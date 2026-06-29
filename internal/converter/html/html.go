// Package html renders markdown to a complete, styled HTML document.
// Importing it (for side effects) registers the "html" format. Its Render
// function is also reused by the browser-based PDF fallback.
//
// Fenced code blocks tagged `mermaid` are emitted as <pre class="mermaid">
// elements and, when present, the document inlines the mermaid library so the
// diagrams render client-side (in a browser, or in the headless-browser PDF
// renderer).
package html

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rapatao/md2/internal/converter"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
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
// basic styling (readable body, bordered tables, code blocks). When mermaid
// rendering is enabled and the source contains mermaid diagrams, the mermaid
// library and an init script are inlined so the diagrams render without any
// network access.
func Render(src []byte) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
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

	var out bytes.Buffer
	out.WriteString(docHead)
	out.Write(body.Bytes())
	if hasMermaid {
		out.WriteString(mermaidScript)
	}
	out.WriteString(docTail)
	return out.Bytes(), nil
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
pre.mermaid{background:none;padding:0;text-align:center}
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
