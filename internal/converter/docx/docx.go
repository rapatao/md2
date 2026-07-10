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
// numbering, nested), code blocks, blockquotes, thematic breaks, GFM tables,
// bold/italic, inline code, links and local images are supported.
//
// Local images referenced by relative paths are embedded as media parts,
// resolved against the input file's directory (via the optional PathConverter).
// Remote (http(s)://) and data: image references, and enabled diagrams
// (mermaid/d2/plantuml), are out of scope — a remote/broken image falls back to
// its alt text, and a diagram renders as its fenced code block.
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	// Register the decoders DecodeConfig needs to read image dimensions.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/html"
	"github.com/rapatao/md2/internal/urlref"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
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

// renderBlock maps a block-level node to WordprocessingML, appending to the body.
// quote is the current blockquote nesting depth (0 = not quoted).
func (b *builder) renderBlock(n ast.Node, src []byte, quote int) {
	switch t := n.(type) {
	case *ast.Heading:
		pPr := fmt.Sprintf(`<w:pStyle w:val="Heading%d"/>`, clampLevel(t.Level))
		b.writeParagraph(pPr, b.inlineChildren(n, src, runStyle{}))
	case *ast.Paragraph, *ast.TextBlock:
		b.writeParagraph(quotePPr(quote), b.inlineChildren(n, src, runStyle{}))
	case *ast.Blockquote:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			b.renderBlock(c, src, quote+1)
		}
	case *ast.List:
		b.renderList(t, src, 0, quote)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		b.codeBlock(n, src)
	case *ast.ThematicBreak:
		b.writeParagraph(`<w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="auto"/></w:pBdr>`, "")
	case *east.Table:
		b.table(n, src)
	case *ast.HTMLBlock:
		// Raw HTML is dropped (the html/text renderers escape or strip it too).
	default:
		b.writeParagraph(quotePPr(quote), b.inlineChildren(n, src, runStyle{}))
	}
}

// writeParagraph appends a <w:p> with the given (already-ordered) pPr fragment
// and run content. An empty pPr or content is omitted.
func (b *builder) writeParagraph(pPr, content string) {
	b.body.WriteString("<w:p>")
	if pPr != "" {
		b.body.WriteString("<w:pPr>")
		b.body.WriteString(pPr)
		b.body.WriteString("</w:pPr>")
	}
	b.body.WriteString(content)
	b.body.WriteString("</w:p>")
}

// renderList emits one list; ordered lists get a fresh numId so each restarts at
// 1, bullet lists share numId 1. ilvl is the nesting depth.
func (b *builder) renderList(list *ast.List, src []byte, ilvl, quote int) {
	numID := 1 // bullets
	if list.IsOrdered() {
		numID = b.nextNumID
		b.nextNumID++
		b.orderedNums = append(b.orderedNums, numID)
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		b.renderListItem(item, src, numID, ilvl, quote)
	}
}

func (b *builder) renderListItem(item ast.Node, src []byte, numID, ilvl, quote int) {
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		switch cc := c.(type) {
		case *ast.List:
			b.renderList(cc, src, ilvl+1, quote)
		case *ast.Paragraph, *ast.TextBlock:
			pPr := fmt.Sprintf(`<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`, ilvl, numID)
			b.writeParagraph(pPr, b.inlineChildren(c, src, runStyle{}))
		default:
			// Other block content inside an item (code block, blockquote, ...).
			b.renderBlock(c, src, quote)
		}
	}
}

// DiagramRasterizer renders a diagram's fenced source to a PNG in the given
// diagram language ("mermaid"/"d2"/"plantuml"), for embedding in the document.
// Wired by the chrome package (which owns the headless browser); nil when no
// browser backend is linked, in which case enabled diagrams stay code blocks.
// Mirrors html.Rasterizer's inversion so docx does not import chrome.
var DiagramRasterizer func(source []byte, kind string) ([]byte, error)

// codeStyle is the chroma theme used to color fenced code blocks — the same
// light "github" style the html and pure-Go PDF paths use, so output is
// consistent across formats.
var codeStyle = styles.Get("github")

// codeBlockPPr styles a fenced code block as one distinct boxed element: a
// border on all four sides plus a light fill, so it reads as a code box rather
// than loose monospace text. The whole block is a single paragraph (lines joined
// by <w:br/>), so the border wraps the block, not each line. Order follows
// CT_PPr: pBdr, shd, spacing.
const codeBlockPPr = `<w:pBdr>` +
	`<w:top w:val="single" w:sz="4" w:space="4" w:color="D0D7DE"/>` +
	`<w:left w:val="single" w:sz="4" w:space="4" w:color="D0D7DE"/>` +
	`<w:bottom w:val="single" w:sz="4" w:space="4" w:color="D0D7DE"/>` +
	`<w:right w:val="single" w:sz="4" w:space="4" w:color="D0D7DE"/>` +
	`</w:pBdr>` +
	`<w:shd w:val="clear" w:color="auto" w:fill="F6F8FA"/>` +
	`<w:spacing w:after="0" w:line="240" w:lineRule="auto"/>`

// codeFont is the run-property fragment giving a run the code monospace font.
const codeFont = `<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/>`

// lineBreak is a run holding a single line break, used to join code lines within
// the one code-block paragraph.
const lineBreak = `<w:r><w:br/></w:r>`

// codeBlock renders a fenced code block. An enabled diagram (mermaid/d2/plantuml,
// via -render) is rasterized to an embedded image when a browser backend is
// available; a block whose language chroma recognizes is syntax-highlighted;
// everything else falls back to plain monospace lines.
func (b *builder) codeBlock(n ast.Node, src []byte) {
	lang := ""
	if fc, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(fc.Language(src))
		if DiagramRasterizer != nil && html.DiagramEnabled(lang) {
			// A rendered diagram replaces its source, unless -keep-diagram-source
			// asks to keep the source too (diagram first, then the code block).
			if b.renderDiagram(fc, src, lang) && !html.KeepDiagramSource {
				return
			}
		}
	}

	if lang != "" && b.highlightedCode(rawLines(n, src), lang) {
		return
	}
	b.plainCode(n, src)
}

// highlightedCode tokenizes the code with chroma and emits the block as one
// boxed paragraph, each token a colored run and lines joined by <w:br/>. It
// returns false (so the caller falls back to plain rendering) when chroma has no
// lexer for the language or tokenizing fails — a lexer glitch must never fail
// the whole conversion.
func (b *builder) highlightedCode(raw []byte, lang string) bool {
	lexer := lexers.Get(lang)
	if lexer == nil {
		return false
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, string(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: highlight failed for %q: %v\n", lang, err)
		return false
	}
	// A token's value can span newlines; split on them and join lines with a
	// break run so the whole block stays one bordered paragraph.
	var runs strings.Builder
	for _, tok := range it.Tokens() {
		entry := codeStyle.Get(tok.Type)
		for i, seg := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				runs.WriteString(lineBreak)
			}
			if seg != "" {
				runs.WriteString(codeRun(entry, seg))
			}
		}
	}
	// The final source newline adds a trailing break with no content; drop it.
	b.writeParagraph(codeBlockPPr, strings.TrimSuffix(runs.String(), lineBreak))
	return true
}

// plainCode renders the block as one boxed, monospace paragraph (no coloring),
// lines joined by <w:br/> — for indented blocks and languages chroma doesn't know.
func (b *builder) plainCode(n ast.Node, src []byte) {
	var runs strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		if i > 0 {
			runs.WriteString(lineBreak)
		}
		seg := lines.At(i)
		text := strings.TrimRight(string(seg.Value(src)), "\n")
		runs.WriteString("<w:r><w:rPr>" + codeFont + "</w:rPr>" + textElem(text) + "</w:r>")
	}
	b.writeParagraph(codeBlockPPr, runs.String())
}

// codeRun renders one highlighted token as a monospace run carrying the chroma
// style entry's color and bold/italic/underline. rPr child order follows CT_RPr:
// rFonts, b, i, color, u.
func codeRun(e chroma.StyleEntry, text string) string {
	var p strings.Builder
	p.WriteString(codeFont)
	if e.Bold == chroma.Yes {
		p.WriteString("<w:b/>")
	}
	if e.Italic == chroma.Yes {
		p.WriteString("<w:i/>")
	}
	if e.Colour.IsSet() {
		p.WriteString(`<w:color w:val="` + colourHex(e.Colour) + `"/>`)
	}
	if e.Underline == chroma.Yes {
		p.WriteString(`<w:u w:val="single"/>`)
	}
	return "<w:r><w:rPr>" + p.String() + "</w:rPr>" + textElem(text) + "</w:r>"
}

// colourHex formats a chroma colour as an uppercase RRGGBB hex string (no '#').
func colourHex(c chroma.Colour) string {
	return fmt.Sprintf("%02X%02X%02X", c.Red(), c.Green(), c.Blue())
}

// renderDiagram rasterizes a diagram fenced block to a PNG and embeds it as its
// own centered paragraph, returning whether it succeeded. A render or decode
// failure warns to stderr and returns false so the caller falls back to code —
// a broken diagram must never fail the whole conversion.
func (b *builder) renderDiagram(fc *ast.FencedCodeBlock, src []byte, lang string) bool {
	png, err := DiagramRasterizer(rawLines(fc, src), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: cannot render %s diagram: %v\n", lang, err)
		return false
	}
	info, ok := b.addImage(png)
	if !ok {
		fmt.Fprintf(os.Stderr, "md2: cannot embed %s diagram: undecodable png\n", lang)
		return false
	}
	b.drawingID++
	b.writeParagraph(`<w:jc w:val="center"/>`, drawingXML(info.rid, info.cx, info.cy, b.drawingID, lang+" diagram"))
	return true
}

// rawLines returns a code block's raw, unescaped content — the source a diagram
// renderer needs.
func rawLines(n ast.Node, src []byte) []byte {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(src))
	}
	return buf.Bytes()
}

// table renders a GFM table as a bordered <w:tbl>. The header row is bold. An
// empty paragraph follows: Word requires a paragraph between/after tables.
func (b *builder) table(n ast.Node, src []byte) {
	cols := 0
	if first := n.FirstChild(); first != nil {
		cols = first.ChildCount()
	}
	b.body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&b.body, `<w:%s w:val="single" w:sz="4" w:space="0" w:color="auto"/>`, edge)
	}
	b.body.WriteString(`</w:tblBorders></w:tblPr><w:tblGrid>`)
	colW := 9360 // content width in twips
	if cols > 0 {
		colW /= cols
	}
	for i := 0; i < cols; i++ {
		fmt.Fprintf(&b.body, `<w:gridCol w:w="%d"/>`, colW)
	}
	b.body.WriteString("</w:tblGrid>")
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		_, header := row.(*east.TableHeader)
		b.body.WriteString("<w:tr>")
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			content := b.inlineChildren(cell, src, runStyle{bold: header})
			b.body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p>`)
			b.body.WriteString(content)
			b.body.WriteString("</w:p></w:tc>")
		}
		b.body.WriteString("</w:tr>")
	}
	b.body.WriteString("</w:tbl><w:p/>")
}

// inlineChildren renders a node's inline children to a runs fragment, carrying
// the active character formatting.
func (b *builder) inlineChildren(n ast.Node, src []byte, style runStyle) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			if v := string(t.Segment.Value(src)); v != "" {
				sb.WriteString(run(style, textElem(v)))
			}
			switch {
			case t.HardLineBreak():
				sb.WriteString(`<w:r><w:br/></w:r>`)
			case t.SoftLineBreak():
				// A markdown soft break is a space, not a forced line break.
				sb.WriteString(run(style, textElem(" ")))
			}
		case *ast.String:
			sb.WriteString(run(style, textElem(string(t.Value))))
		case *ast.Emphasis:
			ns := style
			if t.Level >= 2 {
				ns.bold = true
			} else {
				ns.italic = true
			}
			sb.WriteString(b.inlineChildren(c, src, ns))
		case *ast.CodeSpan:
			ns := style
			ns.code = true
			sb.WriteString(b.inlineChildren(c, src, ns))
		case *ast.Link:
			ns := style
			ns.link = true
			inner := b.inlineChildren(c, src, ns)
			sb.WriteString(b.hyperlink(string(t.Destination), inner))
		case *ast.AutoLink:
			ns := style
			ns.link = true
			url := string(t.URL(src))
			sb.WriteString(b.hyperlink(url, run(ns, textElem(url))))
		case *ast.Image:
			sb.WriteString(b.image(string(t.Destination), plainText(c, src), style))
		case *east.TaskCheckBox:
			mark := "[ ] "
			if t.IsChecked {
				mark = "[x] "
			}
			sb.WriteString(run(style, textElem(mark)))
		case *ast.RawHTML:
			// drop raw inline HTML
		default:
			sb.WriteString(b.inlineChildren(c, src, style))
		}
	}
	return sb.String()
}

// hyperlink wraps inner runs in a w:hyperlink pointing at an external target,
// registering (and de-duplicating) the relationship. An empty destination falls
// back to the plain runs.
func (b *builder) hyperlink(dest, inner string) string {
	if dest == "" {
		return inner
	}
	id, ok := b.hyperlinkIDs[dest]
	if !ok {
		id = b.allocRID()
		b.hyperlinkIDs[dest] = id
		b.rels = append(b.rels, rel{id, relHyperlink, dest, "External"})
	}
	return `<w:hyperlink r:id="` + id + `">` + inner + `</w:hyperlink>`
}

const (
	emuPerPx     = 9525    // 914400 EMU/inch ÷ 96 DPI
	maxImageEMUW = 5943600 // content width: 9360 twips × 635 EMU/twip
	maxImageEMUH = 8229600 // content height: 12960 twips × 635 EMU/twip
)

// image embeds a local image and returns an inline drawing run. Remote, data:,
// unreadable or undecodable references fall back to the alt text so a broken
// reference never fails the conversion (mirrors the epub/html image handling).
func (b *builder) image(src, alt string, style runStyle) string {
	if src == "" || strings.HasPrefix(src, "data:") || urlref.HasScheme(src) {
		return run(style, textElem(alt))
	}
	path := src
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.baseDir, filepath.FromSlash(src))
	}
	info, ok := b.imageByPath[path]
	if !ok {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "md2: cannot embed image %q: %v\n", src, err)
			return run(style, textElem(alt))
		}
		info, ok = b.addImage(data)
		if !ok {
			fmt.Fprintf(os.Stderr, "md2: cannot embed image %q: undecodable\n", src)
			return run(style, textElem(alt))
		}
		b.imageByPath[path] = info
	}
	b.drawingID++
	return drawingXML(info.rid, info.cx, info.cy, b.drawingID, alt)
}

// addImage packages raw image bytes as a media part with an image relationship,
// deriving the file extension and extent (EMU, capped to the content width) from
// the decoded dimensions. ok is false if the bytes are not a decodable image.
func (b *builder) addImage(data []byte) (imgInfo, bool) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return imgInfo{}, false
	}
	name := fmt.Sprintf("img%d.%s", len(b.images)+1, format)
	rid := b.allocRID()
	b.rels = append(b.rels, rel{rid, relImage, "media/" + name, ""})
	b.images = append(b.images, imgFile{name: name, data: data})

	// Fit within the page content box, preserving aspect ratio: cap the width,
	// then the height (a tall diagram can exceed the page height after the width
	// cap, which would span pages).
	cx := int64(cfg.Width) * emuPerPx
	cy := int64(cfg.Height) * emuPerPx
	if cx > maxImageEMUW {
		cy = cy * maxImageEMUW / cx
		cx = maxImageEMUW
	}
	if cy > maxImageEMUH {
		cx = cx * maxImageEMUH / cy
		cy = maxImageEMUH
	}
	return imgInfo{rid: rid, cx: cx, cy: cy}, true
}

// documentRels builds word/_rels/document.xml.rels: the fixed styles and
// numbering relationships plus every hyperlink/image discovered during the walk.
func (b *builder) documentRels() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	sb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	fmt.Fprintf(&sb, `<Relationship Id="rId1" Type=%q Target="styles.xml"/>`, relStyles)
	fmt.Fprintf(&sb, `<Relationship Id="rId2" Type=%q Target="numbering.xml"/>`, relNumbering)
	for _, r := range b.rels {
		sb.WriteString(`<Relationship Id="` + r.id + `" Type="` + r.typ + `" Target="` + escape(r.target) + `"`)
		if r.mode != "" {
			sb.WriteString(` TargetMode="` + r.mode + `"`)
		}
		sb.WriteString("/>")
	}
	sb.WriteString("</Relationships>")
	return sb.String()
}

// numberingXML builds word/numbering.xml: a bullet and an ordered abstractNum
// (nine nesting levels each), num 1 for bullets, and one num per ordered list.
func (b *builder) numberingXML() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	sb.WriteString(`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	sb.WriteString(abstractNum(0, false))
	sb.WriteString(abstractNum(1, true))
	sb.WriteString(`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for _, id := range b.orderedNums {
		fmt.Fprintf(&sb, `<w:num w:numId="%d"><w:abstractNumId w:val="1"/></w:num>`, id)
	}
	sb.WriteString("</w:numbering>")
	return sb.String()
}

// abstractNum defines nine levels of a bullet or ordered (decimal) list.
func abstractNum(id int, ordered bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<w:abstractNum w:abstractNumId="%d">`, id)
	for i := 0; i < 9; i++ {
		numFmt, lvlText := "bullet", "•"
		if ordered {
			numFmt, lvlText = "decimal", fmt.Sprintf("%%%d.", i+1)
		}
		fmt.Fprintf(&sb, `<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/>`+
			`<w:lvlText w:val="%s"/><w:lvlJc w:val="left"/>`+
			`<w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`,
			i, numFmt, lvlText, 720*(i+1))
	}
	sb.WriteString("</w:abstractNum>")
	return sb.String()
}

// run wraps inner content in a <w:r> with run properties for the given style.
func run(style runStyle, inner string) string {
	return "<w:r>" + runProps(style) + inner + "</w:r>"
}

// runProps builds the <w:rPr> for a style (empty when unstyled). Child order
// follows the CT_RPr schema: rFonts, b, i, color, u, shd.
func runProps(style runStyle) string {
	var p strings.Builder
	if style.code {
		p.WriteString(codeFont)
	}
	if style.bold {
		p.WriteString("<w:b/>")
	}
	if style.italic {
		p.WriteString("<w:i/>")
	}
	if style.link {
		p.WriteString(`<w:color w:val="0563C1"/><w:u w:val="single"/>`)
	}
	if style.code {
		p.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F6F8FA"/>`)
	}
	if p.Len() == 0 {
		return ""
	}
	return "<w:rPr>" + p.String() + "</w:rPr>"
}

// textElem wraps text in a whitespace-preserving <w:t>.
func textElem(s string) string {
	return `<w:t xml:space="preserve">` + escape(s) + `</w:t>`
}

// drawingXML builds an inline picture run referencing an embedded image by its
// relationship id, sized to the given EMU extent. alt becomes the descr text.
func drawingXML(rid string, cx, cy int64, id int, alt string) string {
	descr := ""
	if alt != "" {
		descr = ` descr="` + escape(alt) + `"`
	}
	return `<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">` +
		fmt.Sprintf(`<wp:extent cx="%d" cy="%d"/>`, cx, cy) +
		`<wp:effectExtent l="0" t="0" r="0" b="0"/>` +
		fmt.Sprintf(`<wp:docPr id="%d" name="Picture %d"%s/>`, id, id, descr) +
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<pic:pic><pic:nvPicPr>` +
		fmt.Sprintf(`<pic:cNvPr id="%d" name="Picture %d"%s/>`, id, id, descr) +
		`<pic:cNvPicPr/></pic:nvPicPr>` +
		`<pic:blipFill><a:blip r:embed="` + rid + `"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>` +
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/>` +
		fmt.Sprintf(`<a:ext cx="%d" cy="%d"/>`, cx, cy) +
		`</a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>` +
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`
}

// plainText collects a node's descendant text leaves (for image alt text).
func plainText(n ast.Node, src []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(src))
		case *ast.String:
			sb.Write(t.Value)
		default:
			sb.WriteString(plainText(c, src))
		}
	}
	return sb.String()
}

// quotePPr returns the pPr fragment giving a blockquote paragraph its left
// border and indentation (empty when not quoted). Order follows CT_PPr: pBdr
// before ind.
func quotePPr(quote int) string {
	if quote <= 0 {
		return ""
	}
	return `<w:pBdr><w:left w:val="single" w:sz="12" w:space="8" w:color="CCCCCC"/></w:pBdr>` +
		fmt.Sprintf(`<w:ind w:left="%d"/>`, quote*360)
}

func clampLevel(l int) int {
	switch {
	case l < 1:
		return 1
	case l > 6:
		return 6
	default:
		return l
	}
}

// coreXML builds docProps/core.xml. dc:creator is omitted when author is empty.
func coreXML(title, author string) string {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	creator := ""
	if author != "" {
		creator = "<dc:creator>" + escape(author) + "</dc:creator>"
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		"<dc:title>" + escape(title) + "</dc:title>" + creator +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified>` +
		"</cp:coreProperties>"
}

// escape escapes text for XML character data and double-quoted attributes.
var escape = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
).Replace

// Relationship type URIs.
const (
	relStyles    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	relHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	relImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
)

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
  <Default Extension="jpeg" ContentType="image/jpeg"/>
  <Default Extension="gif" ContentType="image/gif"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
  <Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
</Relationships>`

const documentOpen = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`

const sectPr = `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/>` +
	`<w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`

const documentClose = `</w:body></w:document>`

// stylesXML defines Normal, Title and Heading1..6. Each heading carries
// w:outlineLvl so it shows in Word's Navigation pane and feeds an auto-TOC.
const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:before="240" w:after="240"/></w:pPr><w:rPr><w:b/><w:sz w:val="56"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="60"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:color w:val="2F5496"/><w:sz w:val="36"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="60"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:color w:val="2F5496"/><w:sz w:val="32"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:color w:val="2F5496"/><w:sz w:val="28"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:b/><w:i/><w:color w:val="2F5496"/><w:sz w:val="26"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="4"/></w:pPr><w:rPr><w:b/><w:color w:val="2F5496"/><w:sz w:val="24"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="5"/></w:pPr><w:rPr><w:b/><w:i/><w:color w:val="2F5496"/><w:sz w:val="22"/></w:rPr></w:style>
</w:styles>`

func init() {
	converter.Register("docx", Converter{})
}
