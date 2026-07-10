// Package epub renders markdown to a minimal, single-chapter EPUB3 ebook.
// Importing it (for side effects) registers the "epub" format.
//
// The output is an OCF zip container: a stored "mimetype" entry, an OPF package
// document, an EPUB3 navigation document (a TOC of the document's headings), a
// stylesheet, and one XHTML chapter holding the whole document. The chapter is
// rendered by the html package's XHTMLBody — the same pipeline as HTML output —
// so it shares syntax highlighting and d2/plantuml diagrams (inlined as static
// SVG). XHTMLBody emits well-formed XHTML so the chapter passes EPUB validators
// (verified with epubcheck), and the shared base stylesheet (html.BaseCSS) plus
// the chroma highlight styles are written to a packaged style.css, with a
// prefers-color-scheme:dark block so the document stays readable in a reader's
// dark mode. Title and author come from the Title/Author package vars (the
// -title/-author flags), title falling back to the first heading.
//
// mermaid diagrams render client-side via JavaScript, which ebook readers do
// not run, so each is pre-rendered to a static PNG in a headless browser (via
// MermaidRasterizer, wired by the chrome package) and packaged as an image. When
// no browser is available the diagram's source is left in place rather than
// failing the conversion.
//
// Local images referenced by relative paths are packaged as real zip entries
// under OEBPS/images/ and declared in the OPF manifest. Remote (http(s)://) and
// data: image references are left untouched for now — declaring remote
// resources or fetching them is a possible follow-up.
package epub

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/html"
	"github.com/rapatao/md2/internal/urlref"
)

// Author and Title set the EPUB's dc:creator and dc:title metadata, from the
// -author and -title CLI flags. An empty Title falls back to the document's
// first heading (then "Untitled"); an empty Author omits dc:creator.
var (
	Author string
	Title  string
)

// Converter renders markdown source to an EPUB3 document.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	return convert(src, ".", w)
}

// ConvertFrom is Convert with the input file path provided, so relative image
// references can be resolved against its directory and packaged.
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

// Render converts markdown into the bytes of a complete EPUB3 file. Relative
// image references are resolved against baseDir and packaged into the archive.
func Render(src []byte, baseDir string) ([]byte, error) {
	body, css, err := html.XHTMLBody(src)
	if err != nil {
		return nil, err
	}

	// Headings (with the ids html.XHTMLBody already generated) drive both the
	// title fallback and the navigation TOC, so they resolve to the same anchors.
	headings := collectHeadings(body)
	title := Title
	if title == "" {
		if len(headings) > 0 {
			title = headings[0].text
		} else {
			title = "Untitled"
		}
	}

	body, images := packageImages(body, baseDir)
	body, images = rasterizeMermaid(body, images)
	chapter := wrapChapter(title, string(body))

	uid, err := newUUID()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// The mimetype entry must come first and be stored uncompressed (OCF spec).
	mt, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, err
	}
	if _, err := mt.Write([]byte("application/epub+zip")); err != nil {
		return nil, err
	}

	files := []struct{ name, content string }{
		{"META-INF/container.xml", containerXML},
		{"OEBPS/content.opf", opf(uid, title, Author, images)},
		{"OEBPS/nav.xhtml", navXHTML(title, headings)},
		{"OEBPS/style.css", stylesheet(css)},
		{"OEBPS/content.xhtml", chapter},
	}
	for _, f := range files {
		if err := writeEntry(zw, f.name, []byte(f.content)); err != nil {
			return nil, err
		}
	}
	for _, img := range images {
		if err := writeEntry(zw, "OEBPS/"+img.href, img.data); err != nil {
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

// image is a packaged image: its manifest id, archive-relative href (under
// OEBPS/), MIME type, and raw bytes.
type image struct {
	id, href, mime string
	data           []byte
}

var imgSrcRe = regexp.MustCompile(`(<img\b[^>]*?\bsrc=")([^"]*)(")`)

// packageImages rewrites <img src="..."> references that point at local files
// so they point at images packaged under OEBPS/images/, returning the rewritten
// XHTML and the packaged images. Remote (scheme://) and data: references are
// left as-is. A local file that cannot be read is reported to stderr and left
// in place — a broken reference must not fail the conversion.
func packageImages(xhtml []byte, baseDir string) ([]byte, []image) {
	var images []image
	seen := map[string]string{} // resolved path -> href, to package each file once

	out := imgSrcRe.ReplaceAllFunc(xhtml, func(m []byte) []byte {
		g := imgSrcRe.FindSubmatch(m)
		src := string(g[2])
		if src == "" || strings.HasPrefix(src, "data:") || urlref.HasScheme(src) {
			return m
		}

		path := src
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, filepath.FromSlash(src))
		}

		href, ok := seen[path]
		if !ok {
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "md2: cannot package image %q: %v\n", src, err)
				return m
			}
			n := len(images) + 1
			href = fmt.Sprintf("images/img%d%s", n, filepath.Ext(path))
			images = append(images, image{
				id:   fmt.Sprintf("img%d", n),
				href: href,
				mime: imageMIME(path),
				data: data,
			})
			seen[path] = href
		}
		return append(append(append([]byte(nil), g[1]...), []byte(href)...), g[3]...)
	})
	return out, images
}

// MermaidRasterizer renders a mermaid diagram's source to a PNG in the given
// theme ("" for the default/light look, "dark" for a dark variant). It is wired
// by the chrome package (which owns the headless browser); nil when no browser
// backend is linked, in which case mermaid diagrams keep their source. Mirrors
// html.Rasterizer's inversion so epub does not import chrome.
var MermaidRasterizer func(source []byte, theme string) ([]byte, error)

// mermaidPreRe matches a rendered mermaid block: <pre class="mermaid">SOURCE</pre>.
// html.XHTMLBody emits these (HTML-escaped source) for enabled mermaid diagrams.
var mermaidPreRe = regexp.MustCompile(`(?s)<pre class="mermaid">(.*?)</pre>`)

// rasterizeMermaid replaces each rendered mermaid block with packaged PNGs,
// since ebook readers cannot run mermaid's client-side script. Two variants are
// rendered — a default (light) one and a dark-themed one — and emitted as a
// <picture> that switches on prefers-color-scheme, so the diagram stays legible
// in both light and dark reading modes. With no rasterizer (no browser backend)
// the block is left as-is; if only the dark variant fails, the light <img> is
// used alone. A render error never fails the conversion.
// ponytail: two browser launches per diagram (one per variant). Fine for the
// handful of diagrams a doc has; batch into one page if it ever gets slow.
func rasterizeMermaid(body []byte, images []image) ([]byte, []image) {
	if MermaidRasterizer == nil {
		return body, images
	}
	out := mermaidPreRe.ReplaceAllFunc(body, func(m []byte) []byte {
		source := []byte(unescapeXML(string(mermaidPreRe.FindSubmatch(m)[1])))
		light, err := MermaidRasterizer(source, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "md2: cannot render mermaid diagram: %v\n", err)
			return m
		}
		n := len(images) + 1
		lightHref := fmt.Sprintf("images/dgm%d.png", n)
		images = append(images, image{id: fmt.Sprintf("dgm%d", n), href: lightHref, mime: "image/png", data: light})

		dark, derr := MermaidRasterizer(source, "dark")
		if derr != nil {
			// Dark is a nice-to-have; fall back to the single light image.
			fmt.Fprintf(os.Stderr, "md2: cannot render dark mermaid variant: %v\n", derr)
			return []byte(fmt.Sprintf(`<img src=%q alt="diagram"/>`, lightHref))
		}
		darkHref := fmt.Sprintf("images/dgm%d-dark.png", n)
		images = append(images, image{id: fmt.Sprintf("dgm%dd", n), href: darkHref, mime: "image/png", data: dark})
		return []byte(fmt.Sprintf(
			`<picture><source srcset=%q type="image/png" media="(prefers-color-scheme: dark)"/><img src=%q alt="diagram"/></picture>`,
			darkHref, lightHref))
	})
	return out, images
}

// imageMIME guesses an image MIME type from a file extension, defaulting to PNG.
func imageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return "image/png"
	}
}

// heading is a document heading for the navigation TOC: its level (1-6), the id
// html.XHTMLBody assigned it (the in-document anchor), and its plain text.
type heading struct {
	level int
	id    string
	text  string
}

var (
	headingRe = regexp.MustCompile(`(?is)<h([1-6])[^>]*\bid="([^"]*)"[^>]*>(.*?)</h[1-6]>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
)

// collectHeadings pulls the headings out of the already-rendered body, reusing
// the ids html.XHTMLBody generated so the TOC links resolve to the same anchors
// (rather than re-parsing the markdown and risking divergent ids). The label is
// the heading with inline markup stripped.
func collectHeadings(body []byte) []heading {
	var hs []heading
	for _, m := range headingRe.FindAllSubmatch(body, -1) {
		text := strings.TrimSpace(unescapeXML(tagRe.ReplaceAllString(string(m[3]), "")))
		hs = append(hs, heading{level: int(m[1][0] - '0'), id: string(m[2]), text: text})
	}
	return hs
}

// navList renders the headings as a nested <ol> for the EPUB nav document. The
// stack tracks the levels of currently-open lists; output is always well-formed
// even for irregular level sequences (e.g. an h1 followed by an h3).
func navList(hs []heading) string {
	li := func(h heading) string {
		return fmt.Sprintf(`<li><a href="content.xhtml#%s">%s</a>`, h.id, escapeXML(h.text))
	}
	var b strings.Builder
	var open []int // levels of currently-open <ol>s
	for _, h := range hs {
		switch {
		case len(open) == 0 || h.level > open[len(open)-1]:
			b.WriteString("<ol>")
			open = append(open, h.level)
		default:
			b.WriteString("</li>")
			for len(open) > 1 && open[len(open)-1] > h.level {
				b.WriteString("</ol></li>")
				open = open[:len(open)-1]
			}
		}
		b.WriteString(li(h))
	}
	if len(open) > 0 {
		b.WriteString("</li>")
		for len(open) > 1 {
			b.WriteString("</ol></li>")
			open = open[:len(open)-1]
		}
		b.WriteString("</ol>")
	}
	return b.String()
}

// newUUID returns a random RFC 4122 v4 "urn:uuid:" identifier for dc:identifier.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// opf builds the OPF package document: metadata (title, optional creator), a
// manifest of every archive resource (nav, chapter, css, images), and a
// single-item spine.
func opf(uid, title, author string, images []image) string {
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	creator := ""
	if author != "" {
		creator = fmt.Sprintf("\n    <dc:creator>%s</dc:creator>", escapeXML(author))
	}
	var manifest strings.Builder
	for _, img := range images {
		fmt.Fprintf(&manifest, "    <item id=%q href=%q media-type=%q/>\n",
			img.id, img.href, img.mime)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">%s</dc:identifier>
    <dc:title>%s</dc:title>%s
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
%s  </manifest>
  <spine>
    <itemref idref="content"/>
  </spine>
</package>
`, uid, escapeXML(title), creator, modified, manifest.String())
}

// navXHTML builds the EPUB3 navigation document: a TOC of the document's
// headings so readers list its sections. With no headings it falls back to a
// single link to the chapter (an empty nav is invalid).
func navXHTML(title string, headings []heading) string {
	list := navList(headings)
	if list == "" {
		list = fmt.Sprintf(`<ol><li><a href="content.xhtml">%s</a></li></ol>`, escapeXML(title))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en">
<head><meta charset="utf-8"/><title>%s</title></head>
<body>
<nav epub:type="toc" id="toc">
<h1>%s</h1>
%s
</nav>
</body>
</html>
`, escapeXML(title), escapeXML(title), list)
}

// stylesheet builds the packaged style.css: the shared base styling and the
// light syntax-highlight colors, plus a prefers-color-scheme:dark block so the
// document stays readable in a reader's dark mode (dark page, dark code box,
// and github-dark highlight colors). lightChroma is the highlight stylesheet
// html.XHTMLBody returned.
func stylesheet(lightChroma string) string {
	var b strings.Builder
	b.WriteString(html.BaseCSS)
	if lightChroma != "" {
		b.WriteString("\n")
		b.WriteString(forceCodeColor(lightChroma))
	}
	b.WriteString("\n@media (prefers-color-scheme: dark){\n")
	b.WriteString(darkBaseCSS)
	if lightChroma != "" {
		b.WriteString("\n")
		b.WriteString(forceCodeColor(html.ChromaCSS("github-dark")))
	}
	b.WriteString("\n}\n")
	return b.String()
}

// codeColorRe matches a `color: #rrggbb` declaration (but not background-color,
// which is preceded by '-'), capturing the leading delimiter and the value.
var codeColorRe = regexp.MustCompile(`([;{ ]color:\s*)(#[0-9a-fA-F]{3,8})`)

// forceCodeColor duplicates each chroma `color` into `-webkit-text-fill-color`.
// Apple Books' reading themes override `color` (to force readable body text) but
// not `-webkit-text-fill-color`, so this keeps syntax highlighting visible in
// Books' dark/night mode. Applied only to the highlight stylesheet, so ordinary
// prose text stays themeable; it lives in the external stylesheet, so a reader's
// own user stylesheet can still override it.
func forceCodeColor(css string) string {
	return codeColorRe.ReplaceAllString(css, `${1}${2};-webkit-text-fill-color:${2}`)
}

// darkBaseCSS re-colors the base elements for a dark background, mirroring
// html.BaseCSS's structure. Kept in sync with BaseCSS's element set.
const darkBaseCSS = `body{background:#0d1117;color:#c9d1d9}
a{color:#58a6ff}
th,td{border-color:#30363d}
th{background:#161b22}
code,pre,pre.chroma{background:#161b22}
blockquote{border-color:#30363d;color:#8b949e}`

// wrapChapter wraps the rendered body in an XHTML document linking the packaged
// style.css. epubcheck confirms the linked, manifest-declared stylesheet is the
// correct, portable way to style EPUB content; a reader's own theme (e.g. Apple
// Books dark mode) may still override colors, which the dark media query in
// style.css accommodates.
func wrapChapter(title, body string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en">
<head><meta charset="utf-8"/><title>%s</title>
<link rel="stylesheet" type="text/css" href="style.css"/>
</head>
<body>
%s</body>
</html>
`, escapeXML(title), body)
}

// escapeXML escapes text for use in XML character data / attribute values.
var escapeXML = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
).Replace

// unescapeXML reverses the html package's escaping (htmlEscaper: & < > ") to
// recover a mermaid block's original source for the browser to render.
// strings.Replacer scans once, so listing &amp; alongside the others is safe.
var unescapeXML = strings.NewReplacer(
	"&amp;", "&",
	"&lt;", "<",
	"&gt;", ">",
	"&#34;", `"`,
).Replace

func init() {
	converter.Register("epub", Converter{})
}
