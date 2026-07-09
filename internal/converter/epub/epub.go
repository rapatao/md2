// Package epub renders markdown to a minimal, single-chapter EPUB3 ebook.
// Importing it (for side effects) registers the "epub" format.
//
// The output is an OCF zip container: a stored "mimetype" entry, an OPF package
// document, an EPUB3 navigation document, and one XHTML chapter holding the
// whole document. The chapter is rendered as well-formed XHTML (goldmark's
// XHTML mode) so it passes EPUB validators.
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
	"github.com/rapatao/md2/internal/urlref"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"
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

// mdToXHTML renders markdown into an XHTML body fragment. WithXHTML makes void
// elements (<img/>, <br/>, <hr/>) self-closing so the output is well-formed XML;
// without it EPUB validators reject the chapter. No WithUnsafe: raw HTML stays
// escaped, matching the html package.
var mdToXHTML = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(ghtml.WithXHTML()),
)

// Render converts markdown into the bytes of a complete EPUB3 file. Relative
// image references are resolved against baseDir and packaged into the archive.
func Render(src []byte, baseDir string) ([]byte, error) {
	pdoc := mdToXHTML.Parser().Parse(gtext.NewReader(src))
	var body bytes.Buffer
	if err := mdToXHTML.Renderer().Render(&body, src, pdoc); err != nil {
		return nil, err
	}

	title := firstHeading(pdoc, src)
	xhtml, images := packageImages(body.Bytes(), baseDir)
	chapter := wrapChapter(title, string(xhtml))

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
		{"OEBPS/content.opf", opf(uid, title, images)},
		{"OEBPS/nav.xhtml", navXHTML(title)},
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

// firstHeading returns the text of the document's first heading, or "Untitled"
// if there is none.
func firstHeading(doc ast.Node, src []byte) string {
	title := "Untitled"
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if h, ok := n.(*ast.Heading); ok {
				title = string(h.Text(src))
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return title
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

// opf builds the OPF package document: metadata, a manifest of every archive
// resource (nav, chapter, images), and a single-item spine.
func opf(uid, title string, images []image) string {
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	var manifest strings.Builder
	for _, img := range images {
		fmt.Fprintf(&manifest, "    <item id=%q href=%q media-type=%q/>\n",
			img.id, img.href, img.mime)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">%s</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>
%s  </manifest>
  <spine>
    <itemref idref="content"/>
  </spine>
</package>
`, uid, escapeXML(title), modified, manifest.String())
}

func navXHTML(title string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en">
<head><meta charset="utf-8"/><title>%s</title></head>
<body>
<nav epub:type="toc" id="toc">
<h1>%s</h1>
<ol><li><a href="content.xhtml">%s</a></li></ol>
</nav>
</body>
</html>
`, escapeXML(title), escapeXML(title), escapeXML(title))
}

func wrapChapter(title, body string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en">
<head><meta charset="utf-8"/><title>%s</title></head>
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

func init() {
	converter.Register("epub", Converter{})
}
