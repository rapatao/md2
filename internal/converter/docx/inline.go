package docx

import (
	"bytes"
	"fmt"
	"image"
	// Register the decoders DecodeConfig needs to read image dimensions.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/rapatao/md2/internal/urlref"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

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
				sb.WriteString(lineBreak)
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
// deriving the file extension and extent (EMU, capped to the content box) from
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
