// Package pdf renders markdown to PDF. It first tries a pure-Go renderer
// (goldmark-pdf); if that fails — e.g. on tables or glyphs it cannot lay out —
// it falls back to a headless-browser renderer. Importing it (for side
// effects) registers the "pdf" format.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/chrome"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
	gpdf "github.com/stephenafamo/goldmark-pdf"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Converter renders markdown source to a PDF document.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	// Enabled diagrams (e.g. mermaid) render via client-side JavaScript, which
	// the pure-Go renderer cannot run. Go straight to the headless browser so
	// the diagrams appear as SVG rather than raw code.
	if htmlconv.RequiresBrowser(src) {
		return (chrome.Converter{}).Convert(src, w)
	}

	// Render to a buffer first so a partial pure-Go result is never written
	// when we end up falling back to the browser renderer.
	var buf bytes.Buffer
	if err := renderPureGo(src, &buf); err != nil {
		fmt.Fprintf(os.Stderr, "md2: pure-Go PDF failed (%v); trying headless browser...\n", err)
		if ferr := (chrome.Converter{}).Convert(src, w); ferr != nil {
			return fmt.Errorf("pure-Go PDF failed (%v); browser fallback failed: %w", err, ferr)
		}
		return nil
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// renderPureGo renders markdown to PDF using goldmark-pdf. It recovers from
// panics in the underlying gofpdf library and returns them as errors.
func renderPureGo(src []byte, w io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unsupported content: %v", r)
		}
	}()

	// gofpdf's UTF-8 path mishandles characters outside the Basic Multilingual
	// Plane (4-byte UTF-8, e.g. emoji) and panics. It cannot render them anyway,
	// so drop them before handing the source to the renderer.
	src = stripNonBMP(src)

	ctx := context.Background()

	// goldmark-pdf measures table column widths before it sets any font, so a
	// document that opens with a table panics inside gofpdf (no current font).
	// Build the Fpdf ourselves and pre-set a core font to avoid that.
	doc := gpdf.NewFpdf(ctx, gpdf.FpdfConfig{}, nil)
	doc.Fpdf.SetFont("Helvetica", "", 12)

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(
			gpdf.New(
				gpdf.WithContext(ctx),
				gpdf.WithPDF(doc),
			),
		),
	)
	return md.Convert(src, w)
}

// stripNonBMP removes runes outside the Basic Multilingual Plane (U+10000 and
// above), which gofpdf cannot render. Other bytes are preserved unchanged.
func stripNonBMP(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for _, r := range string(src) {
		if r > 0xFFFF {
			continue
		}
		out = utf8.AppendRune(out, r)
	}
	return out
}

func init() {
	converter.Register("pdf", Converter{})
}
