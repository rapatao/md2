package docx

import (
	"fmt"
	"strings"
	"time"
)

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
