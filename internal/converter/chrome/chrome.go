// Package chrome renders markdown to PDF by printing a styled HTML document
// with a headless Chrome/Chromium browser. It is used as a fallback when the
// pure-Go PDF renderer cannot handle a document.
//
// If no browser is installed, one can be downloaded on demand — but only when
// Consent authorizes it.
package chrome

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/rapatao/md2/internal/converter/docx"
	"github.com/rapatao/md2/internal/converter/epub"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// Install the browser-backed hooks into the packages that need them. Those
// packages cannot import chrome (chrome imports them), so the hooks are wired
// here instead: the html -flatten diagram flattener, and the epub mermaid
// renderer (an ebook reader has no JS runtime, so mermaid is pre-rendered to
// inline SVG).
func init() {
	htmlconv.DiagramFlattener = FlattenDiagrams
	epub.MermaidRenderer = RenderMermaidSVG
	docx.DiagramRasterizer = RenderDiagramPNG
}

// mermaidTimeout bounds how long we wait for client-side mermaid rendering to
// finish before printing the PDF anyway.
const mermaidTimeout = 30 * time.Second

// Consent is consulted when no browser is installed and one must be downloaded
// to render the PDF. It must return true to authorize a (~150MB) Chromium
// download. A nil Consent denies the download.
var Consent func() (bool, error)

// Converter renders markdown to PDF via a headless browser.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	return convertFrom(src, ".", w)
}

// ConvertFrom is Convert with the input file path provided, so relative image
// references are resolved against its directory and embedded in the PDF.
func (Converter) ConvertFrom(src []byte, srcPath string, w io.Writer) error {
	return convertFrom(src, filepath.Dir(srcPath), w)
}

func convertFrom(src []byte, baseDir string, w io.Writer) error {
	doc, err := htmlconv.RenderFrom(src, baseDir)
	if err != nil {
		return err
	}

	return withPage(func(page *rod.Page) error {
		if err := page.SetDocumentContent(string(doc)); err != nil {
			return fmt.Errorf("set page content: %w", err)
		}
		if err := page.WaitLoad(); err != nil {
			return fmt.Errorf("wait for page load: %w", err)
		}

		// Mermaid renders diagrams to SVG asynchronously; wait for it to settle
		// before printing so the PDF captures the diagrams, not empty
		// placeholders. d2 diagrams route through the browser too but are already
		// inline SVG at load, so only mermaid needs the wait.
		if htmlconv.RequiresMermaidWait(src) {
			waitMermaid(page)
		}

		stream, err := page.PDF(&proto.PagePrintToPDF{PrintBackground: true})
		if err != nil {
			return fmt.Errorf("print to PDF: %w", err)
		}
		defer stream.Close()

		if _, err := io.Copy(w, stream); err != nil {
			return fmt.Errorf("write PDF: %w", err)
		}
		return nil
	})
}

// withPage launches a headless browser, opens a blank page, and runs fn against
// it. The launcher and browser are always cleaned up — including when setup
// fails partway — so no Chromium process is left behind.
func withPage(fn func(*rod.Page) error) error {
	bin, err := browserPath()
	if err != nil {
		return err
	}

	l := launcher.New().Bin(bin).Headless(true)
	defer l.Cleanup()

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("connect to browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return fmt.Errorf("open page: %w", err)
	}
	return fn(page)
}

// waitMermaid polls until the page's mermaid init script signals completion
// (via window.__md2MermaidDone) or the timeout elapses. A timeout is not fatal:
// the PDF is still printed with whatever has rendered so far.
func waitMermaid(page *rod.Page) {
	deadline := time.Now().Add(mermaidTimeout)
	for time.Now().Before(deadline) {
		res, err := page.Eval(`() => window.__md2MermaidDone === true`)
		if err == nil && res.Value.Bool() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// FlattenDiagrams loads a rendered HTML document in a headless browser, lets the
// inlined mermaid script draw every diagram, strips the now-useless scripts, and
// returns the resulting static, self-contained HTML. It is installed as
// html.DiagramFlattener to back the -flatten path.
//
// This is the PDF path's mechanism: the browser draws each diagram as vector SVG
// and that SVG is what the output keeps. Nothing is measured, clipped or
// snapshotted, so no diagram can come out cut off at an edge, and it stays
// resolution-independent — where a raster snapshot has to pick a scale.
func FlattenDiagrams(doc []byte) (out []byte, err error) {
	err = withPage(func(page *rod.Page) error {
		if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
			Width: 1280, Height: 1024,
		}); err != nil {
			return fmt.Errorf("set viewport: %w", err)
		}
		if err := page.SetDocumentContent(string(doc)); err != nil {
			return fmt.Errorf("set page content: %w", err)
		}
		if err := page.WaitLoad(); err != nil {
			return fmt.Errorf("wait for page load: %w", err)
		}

		els, err := page.Elements("pre.mermaid")
		if err != nil {
			return fmt.Errorf("find diagrams: %w", err)
		}

		// Mermaid renders diagrams to SVG asynchronously; wait for it to settle
		// before reading the document back, so the output carries the drawn
		// diagrams and not empty placeholders. A document without mermaid blocks
		// (e.g. only d2, already inline SVG) has nothing to wait for, so skip the
		// wait rather than eat its timeout.
		if len(els) > 0 {
			waitMermaid(page)
		}

		// The mermaid library and init script are dead weight in a static document.
		if _, err := page.Eval(`() => {
			document.querySelectorAll('script').forEach((s) => s.remove());
		}`); err != nil {
			return fmt.Errorf("strip scripts: %w", err)
		}

		html, err := page.HTML()
		if err != nil {
			return fmt.Errorf("read rendered html: %w", err)
		}
		out = []byte(html)
		return nil
	})
	return out, err
}

// RenderMermaidSVG renders a single mermaid diagram's source to standalone SVG
// markup, for the EPUB converter (installed as epub.MermaidRenderer). It loads
// the diagram in a headless browser, lets the mermaid script draw it, and reads
// back the rendered <svg> element's markup for inlining — vector content the way
// d2/plantuml are, which ebook readers do not dim in dark mode the way they do a
// raster <img>. Any error (including no browser available) is returned so the
// caller can fall back to leaving the diagram source in place.
func RenderMermaidSVG(source []byte, theme string) ([]byte, error) {
	doc := htmlconv.MermaidStandalonePage(source, theme)
	var svg []byte
	err := withPage(func(page *rod.Page) error {
		if err := page.SetDocumentContent(string(doc)); err != nil {
			return fmt.Errorf("set page content: %w", err)
		}
		if err := page.WaitLoad(); err != nil {
			return fmt.Errorf("wait for page load: %w", err)
		}
		waitMermaid(page)
		el, err := page.Element("pre.mermaid svg")
		if err != nil {
			return fmt.Errorf("find diagram: %w", err)
		}
		markup, err := el.HTML()
		if err != nil {
			return fmt.Errorf("read diagram svg: %w", err)
		}
		svg = []byte(markup)
		return nil
	})
	return svg, err
}

// RenderDiagramPNG renders a diagram's fenced source to a PNG for the DOCX
// converter (installed as docx.DiagramRasterizer). d2/plantuml compile to SVG
// in-process (via the html package); mermaid draws client-side in the browser.
// Either way the diagram is loaded on a white page and the rendered vector is
// snapshotted to a raster PNG — the portable form every Word/Office viewer shows
// (unlike SVG, which needs a fallback). Any error (including no browser) is
// returned so the caller falls back to leaving the diagram as a code block.
func RenderDiagramPNG(source []byte, kind string) (png []byte, err error) {
	var doc []byte
	selector := "svg"
	waitForMermaid := false
	switch kind {
	case "mermaid":
		doc = htmlconv.MermaidStandalonePage(source, "")
		selector, waitForMermaid = "pre.mermaid", true
	case "d2":
		svg, e := htmlconv.RenderD2(source, false)
		if e != nil {
			return nil, e
		}
		doc = diagramPage(svg)
	case "plantuml":
		svg, e := htmlconv.RenderPlantUML(source, false)
		if e != nil {
			return nil, e
		}
		doc = diagramPage(svg)
	default:
		return nil, fmt.Errorf("unknown diagram %q", kind)
	}

	err = withPage(func(page *rod.Page) error {
		if err := page.SetDocumentContent(string(doc)); err != nil {
			return fmt.Errorf("set page content: %w", err)
		}
		if err := page.WaitLoad(); err != nil {
			return fmt.Errorf("wait for page load: %w", err)
		}
		if waitForMermaid {
			waitMermaid(page)
		}
		// Opaque white backdrop so the snapshot is legible on the document page.
		if _, err := page.Eval(`() => { document.body.style.background = '#fff'; }`); err != nil {
			return fmt.Errorf("set background: %w", err)
		}
		el, err := page.Element(selector)
		if err != nil {
			return fmt.Errorf("find diagram: %w", err)
		}
		png, err = snapshotDiagram(page, el)
		return err
	})
	return png, err
}

// diagramPage wraps standalone SVG markup (d2/plantuml) in a minimal white HTML
// page for the browser to load and snapshot.
func diagramPage(svg []byte) []byte {
	return []byte(`<!DOCTYPE html><html><head><meta charset="utf-8">` +
		`<style>body{margin:0;background:#fff}svg{display:block}</style></head><body>` +
		string(svg) + `</body></html>`)
}

// diagramScale renders diagram snapshots at this device-pixel ratio so the PNGs
// stay crisp when displayed in the document.
const diagramScale = 2

// maxDiagramViewport bounds the viewport the diagram is laid out in, in CSS
// pixels, so a runaway diagram cannot ask the browser for an unbounded surface.
const maxDiagramViewport = 8192

// maxCaptureSide bounds a snapshot's pixel dimensions. Past roughly this, the
// browser stops growing its capture surface and returns an image cut off at the
// right and bottom, so an outsized diagram is rendered at a lower device scale
// (softer, but whole) rather than clipped.
const maxCaptureSide = 16384

// snapshotDiagram captures a single rendered diagram as a PNG, for the DOCX
// converter (Word needs a raster; HTML keeps the SVG instead — see
// FlattenDiagrams).
//
// The diagram is pinned to the top-left of a viewport sized to it and scaled to
// fit, so the capture region is the whole viewport: (0,0,w,h). That is the same
// rectangle under every screenshot coordinate convention, whether or not the
// browser honours CaptureBeyondViewport and wherever the page happens to be
// scrolled — the clip cannot drift onto the surrounding page or off an edge of
// the diagram, which is what cut the top, bottom and right off earlier
// snapshots. It also fixes resolution: mermaid emits width:100% SVGs, so in a
// window-sized viewport a large diagram was laid out squeezed and snapshotted
// blurry (a 9400px-wide flowchart at 2368x12).
func snapshotDiagram(page *rod.Page, pre *rod.Element) ([]byte, error) {
	// Prefer the rendered <svg>: it has a tight bounding box, avoiding the wide
	// whitespace of the centered <pre>. Fall back to the <pre> if mermaid did
	// not produce an svg (e.g. a diagram with a syntax error).
	target := pre
	if svg, err := pre.Element("svg"); err == nil {
		target = svg
	}

	nat, err := target.Eval(`() => {
		const vb = this.viewBox && this.viewBox.baseVal;
		const r = this.getBoundingClientRect();
		return {
			w: (vb && vb.width) ? vb.width : r.width,
			h: (vb && vb.height) ? vb.height : r.height,
			scalable: !!(vb && vb.width && vb.height),
		};
	}`)
	if err != nil {
		return nil, fmt.Errorf("measure diagram: %w", err)
	}
	natW, natH := nat.Value.Get("w").Num(), nat.Value.Get("h").Num()

	// A diagram past the viewport bound is drawn smaller so it still fits whole.
	// Only a diagram with a viewBox can be scaled by CSS width alone without
	// distorting it; without one the browser gets the natural size and the
	// viewport bound does the clamping.
	fit := 1.0
	if nat.Value.Get("scalable").Bool() {
		fit = math.Min(1, math.Min(maxDiagramViewport/natW, maxDiagramViewport/natH))
	}
	w, h := natW*fit, natH*fit

	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             viewportDim(w),
		Height:            viewportDim(h),
		DeviceScaleFactor: captureScale(w, h),
	}); err != nil {
		return nil, fmt.Errorf("size viewport to diagram: %w", err)
	}

	// Pin the diagram alone at the viewport origin, on an opaque white backdrop
	// so it stays legible wherever it lands. Fixed positioning takes it out of
	// the page flow, so nothing around it can shift it or bleed into the shot.
	if _, err := target.Eval(`(w, scalable) => {
		this.style.cssText = 'position:fixed;left:0;top:0;margin:0;padding:0;background:#fff;max-width:none;max-height:none'
			+ (scalable ? ';width:' + w + 'px;height:auto' : '');
	}`, w, nat.Value.Get("scalable").Bool()); err != nil {
		return nil, fmt.Errorf("pin diagram: %w", err)
	}

	box, err := measureAfterResize(target, viewportDim(w))
	if err != nil {
		return nil, err
	}

	png, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format:                proto.PageCaptureScreenshotFormatPng,
		CaptureBeyondViewport: true,
		Clip:                  &box,
	})
	if err != nil {
		return nil, fmt.Errorf("capture diagram: %w", err)
	}
	return png, nil
}

// resizeTimeout bounds the wait for a viewport resize to reach the page.
const resizeTimeout = 5 * time.Second

// measureAfterResize returns the target's box in page coordinates, once the page
// has taken the new viewport width. Emulation.setDeviceMetricsOverride is
// applied asynchronously, so measuring straight after it can return the box from
// the old layout — and the capture then clips the wrong region: shifted off the
// diagram, taking in the surrounding page at one edge and losing the diagram at
// the other. Waiting for window.innerWidth to report the new width pins the
// measurement to the layout the screenshot will see.
func measureAfterResize(target *rod.Element, width int) (proto.PageViewport, error) {
	deadline := time.Now().Add(resizeTimeout)
	for {
		res, err := target.Eval(`(want) => {
			if (window.innerWidth !== want) return null;
			const r = this.getBoundingClientRect();
			return {x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height};
		}`, width)
		if err != nil {
			return proto.PageViewport{}, fmt.Errorf("measure diagram: %w", err)
		}
		if box := res.Value; !box.Nil() {
			return proto.PageViewport{
				X:      box.Get("x").Num(),
				Y:      box.Get("y").Num(),
				Width:  box.Get("w").Num(),
				Height: box.Get("h").Num(),
				// The device scale factor already renders at diagramScale.
				Scale: 1,
			}, nil
		}
		if time.Now().After(deadline) {
			return proto.PageViewport{}, fmt.Errorf("measure diagram: viewport still not %dpx wide after %s", width, resizeTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// captureScale is the device pixel ratio to snapshot a diagram of the given CSS
// size at: diagramScale, reduced for a diagram whose snapshot would otherwise
// run past maxCaptureSide.
func captureScale(w, h float64) float64 {
	side := math.Max(w, h)
	if side*diagramScale <= maxCaptureSide {
		return diagramScale
	}
	// A diagram bigger than the surface limit at 1:1 cannot be captured whole
	// whatever we do; floor the scale so the browser still gets a sane value.
	return math.Max(maxCaptureSide/side, 0.1)
}

// viewportDim rounds a measured diagram dimension up to a usable viewport
// extent: at least 1 pixel (a zero dimension means "no override" to Chrome) and
// at most maxDiagramViewport.
func viewportDim(v float64) int {
	d := int(math.Ceil(v))
	if d < 1 {
		return 1
	}
	if d > maxDiagramViewport {
		return maxDiagramViewport
	}
	return d
}

// browserGetter downloads a browser on demand, returning its path.
// *launcher.Browser satisfies it.
type browserGetter interface {
	Get() (string, error)
}

// browserPath returns the path to a usable browser: an already-installed one,
// a previously downloaded one, or a freshly downloaded one (with consent).
func browserPath() (string, error) {
	// Prefer a browser already installed on the system.
	if path, has := launcher.LookPath(); has {
		return path, nil
	}

	// Reuse a Chromium we downloaded on an earlier run, if present.
	b := launcher.NewBrowser()
	if b.Validate() == nil {
		return b.BinPath(), nil
	}

	// None available — downloading requires explicit consent.
	return downloadBrowser(b)
}

// downloadBrowser asks Consent and, only if granted, downloads a browser.
// A nil or declining Consent yields an error without downloading.
func downloadBrowser(b browserGetter) (string, error) {
	allow := false
	if Consent != nil {
		var err error
		if allow, err = Consent(); err != nil {
			return "", err
		}
	}
	if !allow {
		return "", fmt.Errorf("no Chrome/Chromium found and download not authorized (re-run with -allow-download to render the document)")
	}
	return b.Get()
}
