// Package chrome renders markdown to PDF by printing a styled HTML document
// with a headless Chrome/Chromium browser. It is used as a fallback when the
// pure-Go PDF renderer cannot handle a document.
//
// If no browser is installed, one can be downloaded on demand — but only when
// Consent authorizes it.
package chrome

import (
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/rapatao/md2/internal/converter/epub"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// Install the browser-backed hooks into the packages that need them. Those
// packages cannot import chrome (chrome imports them), so the hooks are wired
// here instead: the html -flatten diagram rasterizer, and the epub mermaid
// rasterizer (an ebook reader has no JS runtime, so mermaid is pre-rendered to
// a static image).
func init() {
	htmlconv.Rasterizer = Rasterize
	epub.MermaidRasterizer = RasterizeMermaid
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

// Rasterize loads a rendered HTML document in a headless browser, lets the
// inlined mermaid script draw every diagram to SVG, replaces each
// <pre class="mermaid"> with an <img> holding a PNG snapshot, strips the now-
// useless scripts, and returns the resulting static, self-contained HTML. It is
// installed as html.Rasterizer to back the -flatten path.
func Rasterize(doc []byte) (out []byte, err error) {
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
		// before snapshotting so we capture the diagrams, not empty placeholders.
		// A document without mermaid blocks (e.g. only d2, already inline SVG) has
		// nothing to wait for, so skip the wait rather than eat its timeout.
		if len(els) > 0 {
			waitMermaid(page)
		}

		// Force a white page background so diagram snapshots carry an opaque white
		// backdrop rather than transparency, keeping them legible wherever they land.
		if _, err := page.Eval(`() => { document.body.style.background = '#fff'; }`); err != nil {
			return fmt.Errorf("set background: %w", err)
		}
		for _, el := range els {
			png, err := snapshotDiagram(page, el)
			if err != nil {
				return err
			}
			uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			// Replace the rendered <pre class="mermaid"> with a plain <img>. rod
			// binds `this` to the element, so the arrow function can act on it.
			if _, err := el.Eval(`(src) => {
				const img = document.createElement('img');
				img.src = src;
				this.replaceWith(img);
			}`, uri); err != nil {
				return fmt.Errorf("inline diagram: %w", err)
			}
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

// RasterizeMermaid renders a single mermaid diagram's source to a PNG snapshot,
// for the EPUB converter (installed as epub.MermaidRasterizer). It loads the
// diagram in a headless browser, lets the mermaid script draw it, and captures
// the rendered SVG. Any error (including no browser available) is returned so
// the caller can fall back to leaving the diagram source in place.
func RasterizeMermaid(source []byte) ([]byte, error) {
	doc := htmlconv.MermaidStandalonePage(source)
	var png []byte
	err := withPage(func(page *rod.Page) error {
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
		waitMermaid(page)
		// Opaque white backdrop so the snapshot stays legible wherever it lands.
		if _, err := page.Eval(`() => { document.body.style.background = '#fff'; }`); err != nil {
			return fmt.Errorf("set background: %w", err)
		}
		el, err := page.Element("pre.mermaid")
		if err != nil {
			return fmt.Errorf("find diagram: %w", err)
		}
		png, err = snapshotDiagram(page, el)
		return err
	})
	return png, err
}

// diagramScale renders diagram snapshots at this device-pixel ratio so the PNGs
// stay crisp when displayed in the document.
const diagramScale = 2

// snapshotDiagram captures a single rendered mermaid diagram as a PNG. It
// measures the diagram's box and captures exactly that region with an explicit
// clip and CaptureBeyondViewport set, so a diagram larger than the viewport is
// captured in full — rod's Element.Screenshot grabs only the viewport and then
// crops, which clips anything off-screen and mishandles a non-unit scale.
func snapshotDiagram(page *rod.Page, pre *rod.Element) ([]byte, error) {
	// Prefer the rendered <svg>: it has a tight bounding box, avoiding the wide
	// whitespace of the centered <pre>. Fall back to the <pre> if mermaid did
	// not produce an svg (e.g. a diagram with a syntax error).
	target := pre
	if svg, err := pre.Element("svg"); err == nil {
		target = svg
	}

	res, err := target.Eval(`() => {
		const r = this.getBoundingClientRect();
		return {x: r.left + window.scrollX, y: r.top + window.scrollY, w: r.width, h: r.height};
	}`)
	if err != nil {
		return nil, fmt.Errorf("measure diagram: %w", err)
	}
	box := res.Value

	png, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format:                proto.PageCaptureScreenshotFormatPng,
		CaptureBeyondViewport: true,
		Clip: &proto.PageViewport{
			X:      box.Get("x").Num(),
			Y:      box.Get("y").Num(),
			Width:  box.Get("w").Num(),
			Height: box.Get("h").Num(),
			Scale:  diagramScale,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("capture diagram: %w", err)
	}
	return png, nil
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
