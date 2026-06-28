// Package text renders markdown to readable plain text, stripping markup while
// preserving structure (headings, lists, code, tables). Importing it (for side
// effects) registers the "txt" format.
package text

import (
	"fmt"
	"io"
	"strings"

	"github.com/rapatao/md2/internal/converter"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gtext "github.com/yuin/goldmark/text"
)

// Converter renders markdown source to plain text.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	_, err := w.Write(Render(src))
	return err
}

// Render converts markdown into plain text. Block elements are separated by a
// blank line; the result ends with a single newline.
func Render(src []byte) []byte {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).
		Parser().Parse(gtext.NewReader(src))

	blocks := renderBlocks(doc, src)
	out := strings.Join(blocks, "\n\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out)
}

// renderBlocks renders each block-level child of n into its own string.
func renderBlocks(n ast.Node, src []byte) []string {
	var blocks []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if s := renderBlock(c, src); s != "" {
			blocks = append(blocks, s)
		}
	}
	return blocks
}

func renderBlock(n ast.Node, src []byte) string {
	switch t := n.(type) {
	case *ast.Heading:
		return inlineText(n, src)

	case *ast.Paragraph, *ast.TextBlock:
		return inlineText(n, src)

	case *ast.Blockquote:
		return prefixLines(strings.Join(renderBlocks(n, src), "\n\n"), "> ")

	case *ast.List:
		return renderList(t, src)

	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return codeLines(n, src)

	case *ast.ThematicBreak:
		return strings.Repeat("-", 40)

	case *east.Table:
		return renderTable(n, src)

	default:
		// Fall back to any inline text the node carries.
		return inlineText(n, src)
	}
}

// renderList renders ordered/unordered lists, indenting nested content.
func renderList(list *ast.List, src []byte) string {
	var lines []string
	num := list.Start
	if num == 0 {
		num = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "- "
		if list.IsOrdered() {
			marker = fmt.Sprintf("%d. ", num)
			num++
		}
		body := strings.Join(renderBlocks(item, src), "\n")
		lines = append(lines, indentItem(body, marker))
	}
	return strings.Join(lines, "\n")
}

// renderTable renders header and body rows with cells joined by " | ".
func renderTable(n ast.Node, src []byte) string {
	var rows []string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, strings.TrimSpace(inlineText(cell, src)))
		}
		rows = append(rows, strings.Join(cells, " | "))
	}
	return strings.Join(rows, "\n")
}

// codeLines returns the raw text of a code block, verbatim.
func codeLines(n ast.Node, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return strings.TrimRight(b.String(), "\n")
}

// inlineText extracts the visible text of a node's inline children, dropping
// formatting markup. Links keep their destination in parentheses.
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.HardLineBreak() || t.SoftLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.URL(src))
		case *ast.Link:
			txt := inlineText(c, src)
			b.WriteString(txt)
			if dest := string(t.Destination); dest != "" && dest != txt {
				b.WriteString(" (" + dest + ")")
			}
		case *ast.Image:
			b.WriteString("[" + inlineText(c, src) + "]")
		case *ast.RawHTML:
			// drop raw HTML tags
		default:
			b.WriteString(inlineText(c, src))
		}
	}
	return b.String()
}

// prefixLines prepends prefix to every line of s.
func prefixLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// indentItem prefixes the first line with marker and indents continuation
// lines by the marker's width, so nested content lines up under the item.
func indentItem(s, marker string) string {
	lines := strings.Split(s, "\n")
	pad := strings.Repeat(" ", len(marker))
	for i, ln := range lines {
		if i == 0 {
			lines[i] = marker + ln
		} else {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}

func init() {
	converter.Register("txt", Converter{})
}
