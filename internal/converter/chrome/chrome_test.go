package chrome

import (
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
)

// fakeBrowser records whether Get was called and returns a canned result.
type fakeBrowser struct {
	path   string
	err    error
	called bool
}

func (f *fakeBrowser) Get() (string, error) {
	f.called = true
	return f.path, f.err
}

func TestDownloadBrowserConsentGranted(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = func() (bool, error) { return true, nil }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	got, err := downloadBrowser(fb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/chromium" || !fb.called {
		t.Errorf("got (%q, called=%v), want (/tmp/chromium, called=true)", got, fb.called)
	}
}

func TestDownloadBrowserConsentDenied(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = func() (bool, error) { return false, nil }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	if _, err := downloadBrowser(fb); err == nil {
		t.Error("expected error when consent denied, got nil")
	}
	if fb.called {
		t.Error("Get must not be called when consent is denied")
	}
}

func TestDownloadBrowserNilConsent(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	Consent = nil

	fb := &fakeBrowser{path: "/tmp/chromium"}
	if _, err := downloadBrowser(fb); err == nil {
		t.Error("expected error when Consent is nil, got nil")
	}
	if fb.called {
		t.Error("Get must not be called when Consent is nil")
	}
}

func TestDownloadBrowserConsentError(t *testing.T) {
	prev := Consent
	defer func() { Consent = prev }()
	sentinel := errors.New("prompt failed")
	Consent = func() (bool, error) { return false, sentinel }

	fb := &fakeBrowser{path: "/tmp/chromium"}
	_, err := downloadBrowser(fb)
	if !errors.Is(err, sentinel) {
		t.Errorf("want consent error propagated, got %v", err)
	}
	if fb.called {
		t.Error("Get must not be called when consent errors")
	}
}

func TestViewportDim(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int
	}{{0, 1}, {-5, 1}, {10.2, 11}, {maxDiagramViewport + 1, maxDiagramViewport}} {
		if got := viewportDim(tc.in); got != tc.want {
			t.Errorf("viewportDim(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// A diagram far wider than the browser window must be snapshotted at (near) its
// own size, not squeezed into the window and cut off at the clip edge.
func TestRenderDiagramPNGKeepsWideDiagramWhole(t *testing.T) {
	if _, has := launcher.LookPath(); !has {
		t.Skip("no browser installed")
	}

	var src strings.Builder
	src.WriteString("flowchart LR\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&src, "  A%d[Node %d long label text] --> A%d[Node %d long label text]\n", i, i, i+1, i+1)
	}

	png, err := RenderDiagramPNG([]byte(src.String()), "mermaid")
	if err != nil {
		t.Fatalf("render diagram: %v", err)
	}
	cfg, _, err := image.DecodeConfig(strings.NewReader(string(png)))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	// The diagram is ~9000 CSS px wide; a window-sized capture would be ~2400 px
	// across (and a few px tall) at diagramScale.
	if want := maxDiagramViewport; cfg.Width < want {
		t.Errorf("snapshot is %dx%d, want at least %d px wide", cfg.Width, cfg.Height, want)
	}
}
