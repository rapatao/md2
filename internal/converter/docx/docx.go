// Package docx renders markdown to a Word document (.docx). Importing it (for
// side effects) registers the "docx" format.
//
// A .docx is an Office Open XML package: a zip of XML parts. This converter
// hand-builds them with the standard library (archive/zip) — no dependency —
// keeping md2's "pure Go by default" philosophy. The parts are:
//
//   - [Content_Types].xml            declares the content type of each part
//   - _rels/.rels                    package root relationships (→ document, core props)
//   - word/document.xml              the document body (built from the markdown AST)
//   - word/_rels/document.xml.rels   document relationships (styles, numbering,
//     external hyperlinks, embedded images)
//   - word/styles.xml                Normal/Title/Heading1..6 paragraph styles.
//     Headings carry w:outlineLvl so Word's Navigation pane and auto-TOC work.
//   - word/numbering.xml             bullet + ordered list numbering definitions
//   - docProps/core.xml              dc:title / dc:creator (the shared -title/-author)
//   - word/media/img{N}.{ext}        embedded local images
//
// Unlike the epub converter (which reuses the html pipeline), docx maps the
// goldmark AST directly to WordprocessingML — the text converter's block/inline
// switch is the structural template. Headings, paragraphs, lists (native Word
// numbering, nested), code blocks (syntax-highlighted, boxed), blockquotes,
// thematic breaks, GFM tables, bold/italic, inline code, links and local images
// are supported; enabled diagrams (-render) are rasterized to embedded PNGs.
//
// The implementation is split across files within this package:
//
//   - docx.go    the Converter, Render (zip assembly) and the builder type
//   - blocks.go  block-level rendering (headings, lists, code, tables, ...)
//   - inline.go  inline rendering (runs, formatting, links, images, drawings)
//   - parts.go   the static XML part templates and the dynamic part builders
//
// Local images referenced by relative paths are embedded as media parts,
// resolved against the input file's directory (via the optional PathConverter).
// Remote (http(s)://) and data: image references fall back to their alt text.
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/html"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"
)

// Converter renders markdown source to a Word (.docx) document.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	return convert(src, ".", w)
}

// ConvertFrom is Convert with the input file path provided, so relative image
// references can be resolved against its directory and embedded.
func (Converter) ConvertFrom(src []byte, srcPath string, w io.Writer) error {
	return convert(src, filepath.Dir(srcPath), w)
}

func convert(src []byte, baseDir string, w io.Writer) error {
	doc, err := Render(src, baseDir)
	if err != nil {
		return err
	}
	_, err = w.Write(doc)
	return err
}

// Render converts markdown into the bytes of a complete .docx file. Relative
// image references are resolved against baseDir and embedded.
func Render(src []byte, baseDir string) ([]byte, error) {
	b := &builder{
		baseDir:      baseDir,
		nextRID:      3, // rId1 = styles, rId2 = numbering (reserved below)
		nextNumID:    2, // numId 1 = bullets; ordered lists take 2, 3, ...
		imageByPath:  map[string]imgInfo{},
		hyperlinkIDs: map[string]string{},
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	doc := md.Parser().Parse(gtext.NewReader(src))
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		b.renderBlock(c, src, 0)
	}

	var document strings.Builder
	document.WriteString(documentOpen)
	document.WriteString(b.body.String())
	document.WriteString(sectPr)
	document.WriteString(documentClose)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := []struct{ name, content string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"word/document.xml", document.String()},
		{"word/_rels/document.xml.rels", b.documentRels()},
		{"word/styles.xml", stylesXML},
		{"word/numbering.xml", b.numberingXML()},
		{"docProps/core.xml", coreXML(html.DocumentTitle(src), html.Author)},
	}
	for _, p := range parts {
		if err := writeEntry(zw, p.name, []byte(p.content)); err != nil {
			return nil, err
		}
	}
	for _, im := range b.images {
		if err := writeEntry(zw, "word/media/"+im.name, im.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// builder accumulates the document body plus the relationships, images and
// numbering ids discovered while walking the AST.
type builder struct {
	body         strings.Builder
	baseDir      string
	rels         []rel
	images       []imgFile
	imageByPath  map[string]imgInfo // resolved path -> embedded image, packaged once
	hyperlinkIDs map[string]string  // url -> relationship id, so a url is one rel
	nextRID      int
	nextNumID    int
	orderedNums  []int // numIds allocated to ordered lists (each restarts at 1)
	drawingID    int   // unique wp:docPr / pic:cNvPr id per drawing
}

type rel struct{ id, typ, target, mode string }

type imgFile struct {
	name string
	data []byte
}

type imgInfo struct {
	rid    string
	cx, cy int64 // extent in EMU
}

// runStyle is the character formatting in effect for a run, threaded through the
// recursive inline walk.
type runStyle struct{ bold, italic, code, link bool }

func (b *builder) allocRID() string {
	id := fmt.Sprintf("rId%d", b.nextRID)
	b.nextRID++
	return id
}

func init() {
	converter.Register("docx", Converter{})
}
