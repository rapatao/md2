// Package html renders markdown to a complete, styled HTML document.
// Importing it (for side effects) registers the "html" format. Its Render
// function is also reused by the browser-based PDF fallback.
//
// Local images referenced by the document are embedded as data URIs so the
// output is self-contained — it needs no accompanying asset files and survives
// being moved or imported elsewhere.
//
// Fenced code blocks tagged `mermaid` are emitted as <pre class="mermaid">
// elements and, when present, the document inlines the mermaid library so the
// diagrams render client-side (in a browser, or in the headless-browser PDF
// renderer).
package html

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/urlref"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// supportedDiagrams lists the fenced-code languages md2 knows how to render as
// diagrams. Add new renderers (e.g. "plantuml") here together with their
// rendering logic.
var supportedDiagrams = map[string]bool{
	"mermaid": true,
}

// enabledDiagrams is the subset the user turned on via the CLI. It is empty by
// default: a diagram code block renders as plain code unless explicitly
// enabled, so md2's output stays self-contained and lightweight by default.
var enabledDiagrams = map[string]bool{}

// SupportedDiagrams returns the diagram languages that can be enabled, sorted.
func SupportedDiagrams() []string {
	out := make([]string, 0, len(supportedDiagrams))
	for d := range supportedDiagrams {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// EnableDiagrams turns on rendering for the named diagram languages. The
// special name "all" enables every supported renderer. An unknown name returns
// an error and leaves the enabled set unchanged.
func EnableDiagrams(names []string) error {
	add := map[string]bool{}
	for _, n := range names {
		if n == "all" {
			for d := range supportedDiagrams {
				add[d] = true
			}
			continue
		}
		if !supportedDiagrams[n] {
			return fmt.Errorf("unknown diagram renderer %q (have: %v)", n, SupportedDiagrams())
		}
		add[n] = true
	}
	for d := range add {
		enabledDiagrams[d] = true
	}
	return nil
}

// enabledDiagramLang returns the diagram language of a fenced code block when
// md2 can render it and the user enabled it; otherwise "".
func enabledDiagramLang(n *ast.FencedCodeBlock, src []byte) string {
	lang := string(n.Language(src))
	if enabledDiagrams[lang] {
		return lang
	}
	return ""
}

// Flatten controls how enabled diagrams are emitted. When false (default), a
// diagram becomes a <pre class="mermaid"> with the mermaid library inlined, so
// it renders client-side in a browser — interactive, but needing a JS runtime
// to view. When true, the document is rendered in a headless browser and each
// diagram is replaced by a static <img> (a PNG), producing a fully portable
// file that displays anywhere (e.g. imported into Google Docs). Set from the
// -flatten CLI flag.
var Flatten bool

// Rasterizer, if set, flattens a diagram-bearing HTML document to one with
// static <img> diagrams using a headless browser. The chrome package installs
// it via init; html does not import chrome (which imports html) so as to avoid
// an import cycle, hence this indirection.
var Rasterizer func(doc []byte) ([]byte, error)

// ExtraCSS, when non-empty, is appended as an additional <style> block just
// before </head>, after the built-in stylesheet, so it can override or
// extend the defaults using normal CSS cascade rules — it never replaces the
// built-in styling. Applies to HTML output and the browser-rendered PDF
// fallback (both render through RenderFrom); NOT the pure-Go PDF path, which
// has no HTML/CSS layer. Set from the -css CLI flag.
var ExtraCSS string

// Converter renders markdown source to an HTML document.
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
	doc, err := RenderFrom(src, baseDir)
	if err != nil {
		return err
	}

	// With -flatten, replace client-side diagrams with static images so the
	// output is self-contained and needs no JS runtime to view. Only documents
	// that actually contain an enabled diagram need the browser.
	if Flatten && RequiresBrowser(src) {
		if Rasterizer == nil {
			return fmt.Errorf("-flatten needs headless-browser support, which is unavailable")
		}
		if doc, err = Rasterizer(doc); err != nil {
			return err
		}
	}

	_, err = w.Write(doc)
	return err
}

// Render converts markdown into a full HTML document, resolving relative image
// references against the current working directory. See RenderFrom.
func Render(src []byte) ([]byte, error) {
	return RenderFrom(src, ".")
}

// RenderFrom converts markdown into a full, self-contained HTML document with
// basic styling (readable body, bordered tables, code blocks). Local images
// referenced by the document are embedded as data URIs — relative paths are
// resolved against baseDir — so the output stands alone without its asset
// files. When mermaid rendering is enabled and the source contains mermaid
// diagrams, the mermaid library and an init script are inlined so the diagrams
// render without any network access.
func RenderFrom(src []byte, baseDir string) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// Generate GitHub-style id attributes on headings so in-document links
		// like [x](#my-section) resolve to the heading.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(&mermaidRenderer{}, 10)),
		),
	)

	doc := md.Parser().Parse(text.NewReader(src))
	hasMermaid := containsEnabledDiagram(doc, src, "mermaid")

	var body bytes.Buffer
	if err := md.Renderer().Render(&body, src, doc); err != nil {
		return nil, err
	}

	// Embed local images into the body before the mermaid library is appended,
	// so the scan never touches that script (which contains <img>-like strings).
	bodyBytes := inlineLocalImages(body.Bytes(), baseDir)

	var out bytes.Buffer
	out.WriteString(docHeadOpen)
	if ExtraCSS != "" {
		out.WriteString("<style>\n")
		// The extra stylesheet is always wrapped in <style> by us, so a
		// literal "</style>" inside it (invalid as CSS, so only possible via
		// accident or malicious input) must not be able to close the tag early.
		out.WriteString(strings.ReplaceAll(ExtraCSS, "</style>", "<\\/style>"))
		out.WriteString("\n</style>\n")
	}
	out.WriteString(docHeadClose)
	out.Write(bodyBytes)
	if hasMermaid {
		out.WriteString(mermaidScript)
	}
	out.WriteString(docTail)
	return out.Bytes(), nil
}

// imgSrcRe matches the src attribute of an <img> tag, capturing the URL.
var imgSrcRe = regexp.MustCompile(`(<img\b[^>]*?\bsrc=")([^"]*)(")`)

// inlineLocalImages rewrites <img src="..."> references that point at local
// files into self-contained data URIs, resolving relative paths against baseDir.
// Already-inlined (data:) and remote (scheme://) sources are left untouched, as
// is any path that cannot be read — a broken local reference is reported but
// does not fail the render.
func inlineLocalImages(doc []byte, baseDir string) []byte {
	return imgSrcRe.ReplaceAllFunc(doc, func(m []byte) []byte {
		g := imgSrcRe.FindSubmatch(m)
		src := string(g[2])
		if src == "" || strings.HasPrefix(src, "data:") || urlref.HasScheme(src) {
			return m
		}

		path := src
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, filepath.FromSlash(src))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "md2: cannot embed image %q: %v\n", src, err)
			return m
		}
		uri := "data:" + imageMIME(path) + ";base64," + base64.StdEncoding.EncodeToString(data)
		return append(append(append([]byte(nil), g[1]...), uri...), g[3]...)
	})
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

// RequiresBrowser reports whether rendering the source needs a headless
// browser — i.e. it contains an enabled diagram that renders via client-side
// JavaScript. The PDF renderer uses it to skip the pure-Go path (which cannot
// run JavaScript) in favour of the browser. Currently only mermaid qualifies.
func RequiresBrowser(src []byte) bool {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).
		Parser().Parse(text.NewReader(src))
	return containsEnabledDiagram(doc, src, "mermaid")
}

// containsEnabledDiagram walks an already-parsed document for an enabled
// diagram code block of the given language.
func containsEnabledDiagram(doc ast.Node, src []byte, lang string) bool {
	found := false
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if fc, ok := n.(*ast.FencedCodeBlock); ok && enabledDiagramLang(fc, src) == lang {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// mermaidRenderer overrides fenced-code-block rendering so enabled mermaid
// blocks become <pre class="mermaid"> (which the mermaid library turns into
// SVG), while every other code block keeps goldmark's default <pre><code>
// output.
type mermaidRenderer struct{}

func (r *mermaidRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
}

func (r *mermaidRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)

	if enabledDiagramLang(n, source) == "mermaid" {
		w.WriteString(`<pre class="mermaid">`)
		writeLines(w, source, n)
		w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}

	w.WriteString("<pre><code")
	if lang := n.Language(source); lang != nil {
		w.WriteString(` class="language-`)
		htmlEscaper.WriteString(w, string(lang))
		w.WriteString(`"`)
	}
	w.WriteByte('>')
	writeLines(w, source, n)
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// writeLines writes a code block's raw lines, HTML-escaped.
func writeLines(w util.BufWriter, source []byte, n ast.Node) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		htmlEscaper.WriteString(w, string(seg.Value(source)))
	}
}

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&#34;",
)

//go:embed assets/mermaid.min.js
var mermaidJS string

// mermaidScript inlines the mermaid library plus an init script. mermaid.run()
// renders every <pre class="mermaid"> to SVG and resolves a promise; the
// headless PDF renderer waits on the __md2MermaidDone flag before printing.
var mermaidScript = "<script>" +
	// </script> can only appear inside a JS string literal in minified code,
	// where the escaped form is equivalent; neutralise it so it cannot close
	// the surrounding <script> element.
	strings.ReplaceAll(mermaidJS, "</script>", `<\/script>`) +
	"</script>\n" +
	`<script>
mermaid.initialize({startOnLoad:false});
window.__md2Mermaid=mermaid.run()
  .then(function(){window.__md2MermaidDone=true;})
  .catch(function(e){window.__md2MermaidErr=String(e);window.__md2MermaidDone=true;});
</script>
`

const docHeadOpen = `<!DOCTYPE html>
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
pre.mermaid{background:none;padding:0;text-align:center}
blockquote{border-left:4px solid #ddd;margin:0;padding:.2rem 1rem;color:#555}
img{max-width:100%}
</style>
`

const docHeadClose = `</head>
<body>
`

const docTail = `
</body>
</html>
`

func init() {
	converter.Register("html", Converter{})
}
