package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rapatao/md2/internal/converter/html"
)

// readDOCX renders md and returns the archive reader plus a name->contents map.
func readDOCX(t *testing.T, md, baseDir string) (*zip.Reader, map[string]string) {
	t.Helper()
	data, err := Render([]byte(md), baseDir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	return zr, files
}

// assertWellFormed fails if s is not well-formed XML.
func assertWellFormed(t *testing.T, name, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v", name, err)
		}
	}
}

func TestRequiredPartsPresent(t *testing.T) {
	_, files := readDOCX(t, "# Hi\n\nbody\n", ".")
	for _, name := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/_rels/document.xml.rels",
		"word/styles.xml",
		"word/numbering.xml",
		"docProps/core.xml",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing part %s", name)
		}
	}
}

func TestAllPartsWellFormed(t *testing.T) {
	// A document exercising most block/inline kinds, so the XML is non-trivial.
	md := "# Title\n\nHello **bold** _em_ `code` [link](https://x.test).\n\n" +
		"- a\n- b\n\n1. one\n2. two\n\n> quote\n\n```go\nx := 1\n```\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n\n---\n"
	_, files := readDOCX(t, md, ".")
	for name, content := range files {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			assertWellFormed(t, name, content)
		}
	}
}

func TestRenderedTextAndHeadingStyle(t *testing.T) {
	_, files := readDOCX(t, "# Big Title\n\nHello world.\n", ".")
	doc := files["word/document.xml"]
	// goldmark may split a paragraph into per-word text runs, so check the words.
	if !strings.Contains(doc, "Hello") || !strings.Contains(doc, "world.") {
		t.Errorf("body text missing: %q", doc)
	}
	if !strings.Contains(doc, `<w:pStyle w:val="Heading1"/>`) {
		t.Errorf("heading did not use the Heading1 style: %q", doc)
	}
	// The Heading1 style carries outlineLvl so it feeds Word's Navigation pane.
	if !strings.Contains(files["word/styles.xml"], `w:styleId="Heading1"`) {
		t.Errorf("Heading1 style definition missing: %q", files["word/styles.xml"])
	}
}

func TestBoldAndItalicRuns(t *testing.T) {
	_, files := readDOCX(t, "**b** and _i_\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:b/>") {
		t.Errorf("bold run property missing: %q", doc)
	}
	if !strings.Contains(doc, "<w:i/>") {
		t.Errorf("italic run property missing: %q", doc)
	}
}

func TestTableRendered(t *testing.T) {
	_, files := readDOCX(t, "| A | B |\n|---|---|\n| 1 | 2 |\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:tbl>") || !strings.Contains(doc, "<w:tblBorders>") {
		t.Errorf("table not rendered: %q", doc)
	}
	// Header cells are bold.
	if !strings.Contains(doc, "<w:b/>") {
		t.Errorf("table header not bold: %q", doc)
	}
}

func TestLinkRegistersExternalRelationship(t *testing.T) {
	_, files := readDOCX(t, "[go](https://go.dev)\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:hyperlink r:id=") {
		t.Errorf("hyperlink element missing: %q", doc)
	}
	rels := files["word/_rels/document.xml.rels"]
	if !strings.Contains(rels, `Target="https://go.dev"`) || !strings.Contains(rels, `TargetMode="External"`) {
		t.Errorf("external hyperlink relationship missing: %q", rels)
	}
}

func TestDuplicateLinkSharesRelationship(t *testing.T) {
	_, files := readDOCX(t, "[a](https://x.test) [b](https://x.test)\n", ".")
	rels := files["word/_rels/document.xml.rels"]
	if got := strings.Count(rels, `Target="https://x.test"`); got != 1 {
		t.Errorf("identical urls should share one relationship, got %d", got)
	}
}

func TestListEmitsNumbering(t *testing.T) {
	_, files := readDOCX(t, "- a\n- b\n\n1. one\n2. two\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:numPr>") {
		t.Errorf("list paragraphs missing numbering: %q", doc)
	}
	num := files["word/numbering.xml"]
	if !strings.Contains(num, `w:numFmt w:val="bullet"`) || !strings.Contains(num, `w:numFmt w:val="decimal"`) {
		t.Errorf("numbering definitions missing bullet/decimal: %q", num)
	}
	// The ordered list takes a fresh numId (2) so it restarts at 1.
	if !strings.Contains(num, `<w:num w:numId="2">`) {
		t.Errorf("ordered list numId not defined: %q", num)
	}
}

func TestCodeBlockMonospace(t *testing.T) {
	_, files := readDOCX(t, "```go\nfmt.Println(\"hi\")\n```\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, `w:ascii="Consolas"`) {
		t.Errorf("code block not monospaced: %q", doc)
	}
	// Highlighting splits the line into per-token runs, but the string literal
	// stays one token — so its escaped quotes survive contiguously.
	if !strings.Contains(doc, `&quot;hi&quot;`) {
		t.Errorf("code content missing/not escaped: %q", doc)
	}
}

func TestCodeBlockSyntaxHighlighted(t *testing.T) {
	_, files := readDOCX(t, "```go\nfunc main() {}\n```\n", ".")
	doc := files["word/document.xml"]
	// A recognized language gets colored runs (a w:color inside a monospace run).
	if !strings.Contains(doc, "<w:color w:val=") {
		t.Errorf("code block not syntax-highlighted: %q", doc)
	}
	if !strings.Contains(doc, `w:ascii="Consolas"`) {
		t.Errorf("highlighted code not monospaced: %q", doc)
	}
	assertWellFormed(t, "document.xml", doc)
}

func TestUnknownLanguageCodePlain(t *testing.T) {
	// No chroma lexer → plain monospace, no color runs.
	_, files := readDOCX(t, "```\nplain text\n```\n", ".")
	doc := files["word/document.xml"]
	if strings.Contains(doc, "<w:color w:val=") {
		t.Errorf("unlabeled code should not be colored: %q", doc)
	}
	if !strings.Contains(doc, "plain text") {
		t.Errorf("code content missing: %q", doc)
	}
}

func TestTitleFromHeading(t *testing.T) {
	_, files := readDOCX(t, "# My Doc\n\nbody\n", ".")
	if !strings.Contains(files["docProps/core.xml"], "<dc:title>My Doc</dc:title>") {
		t.Errorf("title not derived from H1: %q", files["docProps/core.xml"])
	}
}

func TestTitleAndAuthorMetadata(t *testing.T) {
	html.Title, html.Author = "My Manual", "Jane Doe"
	t.Cleanup(func() { html.Title, html.Author = "", "" })
	_, files := readDOCX(t, "# Ignored\n\nbody\n", ".")
	core := files["docProps/core.xml"]
	if !strings.Contains(core, "<dc:title>My Manual</dc:title>") {
		t.Errorf("-title did not override heading: %q", core)
	}
	if !strings.Contains(core, "<dc:creator>Jane Doe</dc:creator>") {
		t.Errorf("author metadata missing: %q", core)
	}
}

func TestNoCreatorWhenAuthorUnset(t *testing.T) {
	_, files := readDOCX(t, "# H\n\nbody\n", ".")
	if strings.Contains(files["docProps/core.xml"], "<dc:creator>") {
		t.Errorf("dc:creator should be omitted without an author: %q", files["docProps/core.xml"])
	}
}

func TestLocalImageEmbedded(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "pic.png"), 20, 10)

	zr, files := readDOCX(t, "![alt text](pic.png)\n", dir)
	if _, ok := findEntry(zr, "word/media/img1.png"); !ok {
		t.Errorf("image not embedded under word/media/")
	}
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:drawing>") || !strings.Contains(doc, "<a:blip r:embed=") {
		t.Errorf("drawing/blip missing for embedded image: %q", doc)
	}
	if !strings.Contains(files["word/_rels/document.xml.rels"], `Target="media/img1.png"`) {
		t.Errorf("image relationship missing: %q", files["word/_rels/document.xml.rels"])
	}
}

func TestMissingImageFallsBackToAltText(t *testing.T) {
	zr, files := readDOCX(t, "![the alt](nope.png)\n", t.TempDir())
	if _, ok := findEntry(zr, "word/media/img1.png"); ok {
		t.Errorf("no image should be embedded for a missing file")
	}
	if !strings.Contains(files["word/document.xml"], "the alt") {
		t.Errorf("missing image should fall back to alt text: %q", files["word/document.xml"])
	}
}

func TestEnabledDiagramEmbeddedAsImage(t *testing.T) {
	if err := html.EnableDiagrams([]string{"d2"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	// Stub the rasterizer (real one needs a browser) to return a valid PNG.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	pngBytes := buf.Bytes()
	DiagramRasterizer = func(_ []byte, kind string) ([]byte, error) {
		if kind != "d2" {
			return nil, fmt.Errorf("unexpected diagram kind %q", kind)
		}
		return pngBytes, nil
	}
	t.Cleanup(func() { DiagramRasterizer = nil })

	zr, files := readDOCX(t, "# D\n\n```d2\na -> b\n```\n", ".")
	if _, ok := findEntry(zr, "word/media/img1.png"); !ok {
		t.Errorf("enabled diagram not embedded as an image")
	}
	doc := files["word/document.xml"]
	if !strings.Contains(doc, "<w:drawing>") || !strings.Contains(doc, "<a:blip r:embed=") {
		t.Errorf("diagram drawing missing: %q", doc)
	}
	// The rendered diagram replaces its source (not emitted as a code block).
	if strings.Contains(doc, "a -&gt; b") {
		t.Errorf("diagram source should be replaced by the image: %q", doc)
	}
}

func TestDiagramFallsBackToCodeWhenNoRasterizer(t *testing.T) {
	if err := html.EnableDiagrams([]string{"d2"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	DiagramRasterizer = nil // no browser backend linked
	_, files := readDOCX(t, "```d2\na -> b\n```\n", ".")
	doc := files["word/document.xml"]
	if !strings.Contains(doc, `w:ascii="Consolas"`) || !strings.Contains(doc, "a -&gt; b") {
		t.Errorf("diagram should fall back to a code block without a rasterizer: %q", doc)
	}
}

func TestConverterConvertIsValidZip(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("# Hi\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Errorf("Convert output is not a valid zip: %v", err)
	}
}

func findEntry(zr *zip.Reader, name string) (*zip.File, bool) {
	for _, f := range zr.File {
		if f.Name == name {
			return f, true
		}
	}
	return nil, false
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
}
