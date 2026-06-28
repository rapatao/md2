// Package html renders markdown to a complete, styled HTML document.
// Importing it (for side effects) registers the "html" format. Its Render
// function is also reused by the browser-based PDF fallback.
package html

import (
	"bytes"
	"io"

	"github.com/rapatao/md2/internal/converter"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Converter renders markdown source to an HTML document.
type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error {
	doc, err := Render(src)
	if err != nil {
		return err
	}
	_, err = w.Write(doc)
	return err
}

// Render converts markdown into a full, self-contained HTML document with
// basic styling (readable body, bordered tables, code blocks).
func Render(src []byte) ([]byte, error) {
	var body bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := md.Convert(src, &body); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString(docHead)
	out.Write(body.Bytes())
	out.WriteString(docTail)
	return out.Bytes(), nil
}

const docHead = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<style>
body{font-family:-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;line-height:1.5;max-width:48rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
h1,h2,h3,h4{line-height:1.25}
table{border-collapse:collapse;width:100%;margin:1rem 0}
th,td{border:1px solid #ccc;padding:.4rem .6rem;text-align:left;vertical-align:top}
th{background:#f2f2f2}
code{background:#f4f4f4;padding:.1rem .3rem;border-radius:3px;font-family:ui-monospace,Menlo,Consolas,monospace}
pre{background:#f4f4f4;padding:1rem;border-radius:6px;overflow:auto}
pre code{background:none;padding:0}
blockquote{border-left:4px solid #ddd;margin:0;padding:.2rem 1rem;color:#555}
img{max-width:100%}
</style>
</head>
<body>
`

const docTail = `
</body>
</html>
`

func init() {
	converter.Register("html", Converter{})
}
