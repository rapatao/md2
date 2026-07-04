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
	"net/http"
	"os"
	"path/filepath"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/chrome"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
	"github.com/rapatao/md2/internal/merge"
	gpdf "github.com/stephenafamo/goldmark-pdf"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// Converter renders markdown source to a PDF document.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	return convertFrom(src, "", w)
}

// ConvertFrom is Convert with the input file path provided, so the browser
// fallback can resolve relative image references and embed them in the PDF.
func (Converter) ConvertFrom(src []byte, srcPath string, w io.Writer) error {
	return convertFrom(src, srcPath, w)
}

func convertFrom(src []byte, srcPath string, w io.Writer) error {
	// browser renders via the headless-browser engine, passing the input path
	// when known so local images are embedded.
	browser := func() error {
		if srcPath != "" {
			return (chrome.Converter{}).ConvertFrom(src, srcPath, w)
		}
		return (chrome.Converter{}).Convert(src, w)
	}

	// Enabled diagrams need client-side JS, non-BMP runes crash gofpdf, and
	// custom CSS has no effect outside an HTML document — any of these routes
	// straight to the headless browser instead of the pure-Go renderer.
	if needsBrowser(src) {
		return browser()
	}

	// Render to a buffer first so a partial pure-Go result is never written
	// when we end up falling back to the browser renderer.
	var buf bytes.Buffer
	if err := renderPureGo(src, srcPath, &buf); err != nil {
		fmt.Fprintf(os.Stderr, "md2: pure-Go PDF failed (%v); trying headless browser...\n", err)
		if ferr := browser(); ferr != nil {
			return fmt.Errorf("pure-Go PDF failed (%v); browser fallback failed: %w", err, ferr)
		}
		return nil
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// renderPureGo renders markdown to PDF using goldmark-pdf. It recovers from
// panics in the underlying gofpdf library and returns them as errors.
func renderPureGo(src []byte, srcPath string, w io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unsupported content: %v", r)
		}
	}()

	ctx := context.Background()

	// goldmark-pdf measures table column widths before it sets any font, so a
	// document that opens with a table panics inside gofpdf (no current font).
	// Build the Fpdf ourselves and pre-set a core font to avoid that.
	doc := gpdf.NewFpdf(ctx, gpdf.FpdfConfig{}, nil)
	doc.Fpdf.SetFont("Helvetica", "", 12)

	opts := []gpdf.Option{
		gpdf.WithContext(ctx),
		gpdf.WithPDF(doc),
		// Highlight code blocks with the same "github" chroma style the HTML
		// path uses, rather than goldmark-pdf's default "monokai", so output is
		// consistent across the pure-Go and browser-rendered paths.
		gpdf.WithCodeBlockTheme(styles.Get("github")),
	}
	// goldmark-pdf defaults ImageFS to the process's CWD, so relative images
	// only resolve when md2 happens to run from the input file's directory.
	// It also strips the leading "/" off absolute destinations before
	// looking them up (fs.go's localPath), so no single DirFS root can
	// serve both relative and absolute paths except the filesystem root
	// itself. Normalize any remaining relative destinations to absolute
	// first (multi-input merges already did this per-file in merge.Inputs),
	// then root the FS at "/" so both forms resolve.
	if srcPath != "" {
		src = merge.RewriteRelativeImagePaths(src, filepath.Dir(srcPath))
		opts = append(opts, gpdf.WithImageFS(http.FS(os.DirFS("/"))))
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// Emit heading id attributes so goldmark-pdf registers internal-link
		// destinations, making in-document links like [x](#my-section) clickable.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRenderer(gpdf.New(opts...)),
	)
	return md.Convert(src, w)
}

// needsBrowser reports whether src must render through the headless-browser
// path rather than goldmark-pdf: it contains an enabled diagram, a rune
// outside the Basic Multilingual Plane, or custom CSS is set (goldmark-pdf
// has no HTML/CSS layer to apply it to).
func needsBrowser(src []byte) bool {
	return htmlconv.RequiresBrowser(src) || hasNonBMP(src) || htmlconv.ExtraCSS != ""
}

// hasNonBMP reports whether src contains a rune outside the Basic
// Multilingual Plane (U+10000 and above), which gofpdf cannot render.
func hasNonBMP(src []byte) bool {
	for _, r := range string(src) {
		if r > 0xFFFF {
			return true
		}
	}
	return false
}

func init() {
	converter.Register("pdf", Converter{})
}
