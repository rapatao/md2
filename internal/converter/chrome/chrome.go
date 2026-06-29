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
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

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
	bin, err := browserPath()
	if err != nil {
		return err
	}

	doc, err := htmlconv.Render(src)
	if err != nil {
		return err
	}

	url, err := launcher.New().Bin(bin).Headless(true).Launch()
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
	if err := page.SetDocumentContent(string(doc)); err != nil {
		return fmt.Errorf("set page content: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("wait for page load: %w", err)
	}

	// Mermaid renders diagrams to SVG asynchronously; wait for it to settle
	// before printing so the PDF captures the diagrams, not empty placeholders.
	if htmlconv.RequiresBrowser(src) {
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
		return "", fmt.Errorf("no Chrome/Chromium found and download not authorized (re-run with -allow-download)")
	}
	return b.Get()
}
