package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rapatao/md2/internal/converter/html"
)

// readEPUB renders md and returns the archive reader plus a name->contents map.
func readEPUB(t *testing.T, md, baseDir string) (*zip.Reader, map[string]string) {
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

func TestMimetypeFirstAndStored(t *testing.T) {
	zr, _ := readEPUB(t, "# Hi\n\nbody\n", ".")
	first := zr.File[0]
	if first.Name != "mimetype" {
		t.Fatalf("first entry = %q, want mimetype", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype must be stored uncompressed, got method %d", first.Method)
	}
	rc, _ := first.Open()
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "application/epub+zip" {
		t.Errorf("mimetype content = %q", b)
	}
}

func TestRequiredEntriesPresent(t *testing.T) {
	_, files := readEPUB(t, "# Hi\n\nbody\n", ".")
	for _, name := range []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/style.css",
		"OEBPS/content.xhtml",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing entry %s", name)
		}
	}
}

func TestChapterIsWellFormedXHTML(t *testing.T) {
	_, files := readEPUB(t, "# Title\n\nHello **world**.\n\n---\n", ".")
	chapter := files["OEBPS/content.xhtml"]
	// A token loop over the whole document fails on any malformed XML — this is
	// what proves WithXHTML self-closed the <hr/> void element.
	dec := xml.NewDecoder(strings.NewReader(chapter))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("chapter is not well-formed XML: %v", err)
		}
	}
	if !strings.Contains(chapter, "Hello") {
		t.Errorf("rendered text missing from chapter: %q", chapter)
	}
}

func TestTitleFromHeading(t *testing.T) {
	_, files := readEPUB(t, "# My Book\n\nbody\n", ".")
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>My Book</dc:title>") {
		t.Errorf("title not derived from H1: %q", files["OEBPS/content.opf"])
	}
}

func TestTitleFromHeadingWithInlineMarkup(t *testing.T) {
	// The walker must descend through emphasis/code and collect the text leaves.
	_, files := readEPUB(t, "# My **Bold** `Book`\n\nbody\n", ".")
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>My Bold Book</dc:title>") {
		t.Errorf("inline markup not flattened in title: %q", files["OEBPS/content.opf"])
	}
}

func TestTitleFallsBackToUntitled(t *testing.T) {
	_, files := readEPUB(t, "just a paragraph, no heading\n", ".")
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>Untitled</dc:title>") {
		t.Errorf("missing Untitled fallback: %q", files["OEBPS/content.opf"])
	}
}

func TestAuthorAndTitleMetadata(t *testing.T) {
	Author, Title = "Jane Doe", "My Manual"
	t.Cleanup(func() { Author, Title = "", "" })
	_, files := readEPUB(t, "# Ignored Heading\n\nbody\n", ".")
	opf := files["OEBPS/content.opf"]
	if !strings.Contains(opf, "<dc:title>My Manual</dc:title>") {
		t.Errorf("-title did not override heading: %q", opf)
	}
	if !strings.Contains(opf, "<dc:creator>Jane Doe</dc:creator>") {
		t.Errorf("author metadata missing: %q", opf)
	}
}

func TestNoCreatorWhenAuthorUnset(t *testing.T) {
	_, files := readEPUB(t, "# H\n\nbody\n", ".")
	if strings.Contains(files["OEBPS/content.opf"], "<dc:creator>") {
		t.Errorf("dc:creator should be omitted when no author: %q", files["OEBPS/content.opf"])
	}
}

func TestNavListsHeadings(t *testing.T) {
	md := "# Book\n\n## Chapter One\n\n### Sub A\n\n## Chapter Two\n"
	_, files := readEPUB(t, md, ".")
	nav := files["OEBPS/nav.xhtml"]
	for _, id := range []string{"book", "chapter-one", "sub-a", "chapter-two"} {
		if !strings.Contains(nav, `href="content.xhtml#`+id+`"`) {
			t.Errorf("nav missing link to #%s: %q", id, nav)
		}
	}
	// Nesting: Sub A sits in a nested list under Chapter One.
	if !strings.Contains(nav, "<ol><li>") || !strings.Contains(nav, "</ol></li>") {
		t.Errorf("nav TOC is not nested: %q", nav)
	}
	// The nav ids must match the chapter's heading anchors.
	if !strings.Contains(files["OEBPS/content.xhtml"], `id="chapter-one"`) {
		t.Errorf("chapter heading id missing, nav links would dangle")
	}
}

func TestNavWellFormedWithLevelSkips(t *testing.T) {
	// An h1 followed by an h3 (skipping h2) must still yield well-formed XML.
	_, files := readEPUB(t, "# A\n\n### Deep\n\n## Back\n", ".")
	dec := xml.NewDecoder(strings.NewReader(files["OEBPS/nav.xhtml"]))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("nav.xhtml not well-formed with irregular heading levels: %v", err)
		}
	}
}

func TestDarkModeStylesheet(t *testing.T) {
	_, files := readEPUB(t, "```go\nx := 1\n```\n", ".")
	css := files["OEBPS/style.css"]
	if !strings.Contains(css, "@media (prefers-color-scheme: dark)") {
		t.Errorf("dark-mode media query missing: %q", css)
	}
	if !strings.Contains(css, "#0d1117") {
		t.Errorf("dark background missing: %q", css)
	}
}

func TestCodeColorsForcedForAppleBooks(t *testing.T) {
	_, files := readEPUB(t, "```go\nx := 1\n```\n", ".")
	css := files["OEBPS/style.css"]
	// Chroma token colors get -webkit-text-fill-color so Apple Books can't strip
	// them (it overrides `color` but not text-fill-color).
	if !strings.Contains(css, "-webkit-text-fill-color") {
		t.Errorf("code token colors not forced for Apple Books: %q", css)
	}
	// ...but only on token color, never on background-color.
	if regexpMatch(`background-color:\s*#[0-9a-fA-F]+;-webkit-text-fill-color`, css) {
		t.Errorf("text-fill-color wrongly applied to a background: %q", css)
	}
}

func regexpMatch(pattern, s string) bool {
	return regexp.MustCompile(pattern).MatchString(s)
}

func TestLocalImagePackaged(t *testing.T) {
	dir := t.TempDir()
	// A tiny valid PNG isn't needed — packaging only reads and copies bytes.
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, files := readEPUB(t, "![alt](pic.png)\n", dir)

	if got := files["OEBPS/images/img1.png"]; got != "PNGDATA" {
		t.Errorf("image not packaged: %q", got)
	}
	if !strings.Contains(files["OEBPS/content.opf"], `media-type="image/png"`) {
		t.Errorf("image manifest entry missing: %q", files["OEBPS/content.opf"])
	}
	if !strings.Contains(files["OEBPS/content.xhtml"], `src="images/img1.png"`) {
		t.Errorf("image src not rewritten: %q", files["OEBPS/content.xhtml"])
	}
}

func TestMissingImageDoesNotFail(t *testing.T) {
	_, files := readEPUB(t, "![alt](nope.png)\n", t.TempDir())
	// Render succeeds and the original ref is left in place.
	if !strings.Contains(files["OEBPS/content.xhtml"], `src="nope.png"`) {
		t.Errorf("missing image ref should be left as-is: %q", files["OEBPS/content.xhtml"])
	}
	if strings.Contains(files["OEBPS/content.opf"], "images/") {
		t.Errorf("no image should be packaged: %q", files["OEBPS/content.opf"])
	}
}

func TestCodeBlockHighlighted(t *testing.T) {
	_, files := readEPUB(t, "```go\nfmt.Println(\"hi\")\n```\n", ".")
	chapter := files["OEBPS/content.xhtml"]
	if !strings.Contains(chapter, `class="chroma"`) {
		t.Errorf("code block not syntax-highlighted: %q", chapter)
	}
	// Colors come from the packaged, linked stylesheet — the portable way to
	// style EPUB content.
	if !strings.Contains(chapter, `href="style.css"`) {
		t.Errorf("chapter does not link style.css: %q", chapter)
	}
	css := files["OEBPS/style.css"]
	if !strings.Contains(css, ".chroma") {
		t.Errorf("chroma highlight styles missing from style.css: %q", css)
	}
	// Base styling (html.BaseCSS) is bundled into the same stylesheet.
	if !strings.Contains(css, "font-family") {
		t.Errorf("base stylesheet missing from style.css: %q", css)
	}
}

func TestMermaidRasterizedToImage(t *testing.T) {
	if err := html.EnableDiagrams([]string{"mermaid"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	// Distinct bytes per theme so we can tell the variants apart.
	MermaidRasterizer = func(_ []byte, theme string) ([]byte, error) {
		return []byte("PNG-" + theme), nil
	}
	t.Cleanup(func() { MermaidRasterizer = nil })

	_, files := readEPUB(t, "```mermaid\ngraph TD\n  A --> B\n```\n", ".")

	if files["OEBPS/images/dgm1.png"] != "PNG-" {
		t.Errorf("light mermaid variant not packaged: %q", files["OEBPS/images/dgm1.png"])
	}
	if files["OEBPS/images/dgm1-dark.png"] != "PNG-dark" {
		t.Errorf("dark mermaid variant not packaged: %q", files["OEBPS/images/dgm1-dark.png"])
	}
	chapter := files["OEBPS/content.xhtml"]
	// A <picture> switches variants on the reader's color scheme.
	if !strings.Contains(chapter, `<source srcset="images/dgm1-dark.png" type="image/png" media="(prefers-color-scheme: dark)"/>`) {
		t.Errorf("dark <source> missing: %q", chapter)
	}
	if !strings.Contains(chapter, `<img src="images/dgm1.png"`) {
		t.Errorf("light <img> fallback missing: %q", chapter)
	}
	if strings.Contains(chapter, `class="mermaid"`) {
		t.Errorf("mermaid source left in chapter after rasterizing: %q", chapter)
	}
	for _, href := range []string{"images/dgm1.png", "images/dgm1-dark.png"} {
		if !strings.Contains(files["OEBPS/content.opf"], `href="`+href+`" media-type="image/png"`) {
			t.Errorf("mermaid image %s not declared in manifest: %q", href, files["OEBPS/content.opf"])
		}
	}
}

func TestMermaidLeftAsSourceWhenRasterizerFails(t *testing.T) {
	if err := html.EnableDiagrams([]string{"mermaid"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	MermaidRasterizer = func([]byte, string) ([]byte, error) { return nil, errors.New("no browser") }
	t.Cleanup(func() { MermaidRasterizer = nil })

	_, files := readEPUB(t, "```mermaid\ngraph TD\n  A --> B\n```\n", ".")
	chapter := files["OEBPS/content.xhtml"]
	if !strings.Contains(chapter, `class="mermaid"`) {
		t.Errorf("mermaid source should be kept when rasterizing fails: %q", chapter)
	}
	if strings.Contains(chapter, "images/dgm") {
		t.Errorf("no diagram image should be packaged on failure: %q", chapter)
	}
}

func TestDiagramRenderedAsSVG(t *testing.T) {
	if err := html.EnableDiagrams([]string{"d2"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	_, files := readEPUB(t, "# D\n\n```d2\na -> b\n```\n", ".")
	chapter := files["OEBPS/content.xhtml"]
	if !strings.Contains(chapter, "<svg") {
		t.Errorf("d2 diagram not rendered as inline SVG: %q", chapter)
	}
	// Inline SVG must not break XHTML well-formedness.
	dec := xml.NewDecoder(strings.NewReader(chapter))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("chapter with inline SVG is not well-formed XML: %v", err)
		}
	}
}

func TestConverterConvert(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("# Hi\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Errorf("Convert output is not a valid zip: %v", err)
	}
}
