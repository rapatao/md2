package docx

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rapatao/md2/internal/converter/html"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

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
