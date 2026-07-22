// Package html renders markdown to a complete, styled HTML document.
// Importing it (for side effects) registers the "html" format. Its Render
// function is also reused by the browser-based PDF fallback.
//
// Local images referenced by the document are embedded as data URIs so the
// output is self-contained — it needs no accompanying asset files and survives
// being moved or imported elsewhere. Remote (http/https) images stay live
// references by default; with -flatten they are fetched and embedded too, for a
// fully self-contained document (at the cost of needing network access).
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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/urlref"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// supportedDiagrams lists the fenced-code languages md2 knows how to render as
// diagrams. Add new renderers (e.g. "plantuml") here together with their
// rendering logic.
var supportedDiagrams = map[string]bool{
	"mermaid":  true,
	"d2":       true,
	"plantuml": true,
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

// DiagramEnabled reports whether lang is a diagram renderer the user turned on
// via -render. Non-HTML converters (docx) that walk the AST directly use it to
// decide whether a fenced code block should be rendered as a diagram.
func DiagramEnabled(lang string) bool {
	return enabledDiagrams[lang]
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

// KeepDiagramSource, when true, keeps the original fenced diagram source in the
// output in addition to the rendered diagram — the rendered diagram is emitted
// first, immediately followed by the source as a normal code block. Off by
// default (a diagram replaces its source). Set from the -keep-diagram-source flag.
var KeepDiagramSource bool

// ExtraCSS, when non-empty, is appended as an additional <style> block just
// before </head>, after the built-in stylesheet, so it can override or
// extend the defaults using normal CSS cascade rules — it never replaces the
// built-in styling. Applies to HTML output and the browser-rendered PDF
// fallback (both render through RenderFrom); NOT the pure-Go PDF path, which
// has no HTML/CSS layer. Set from the -css CLI flag.
var ExtraCSS string

// Title and Author are document metadata shared across output formats, set from
// the -title and -author CLI flags. They live here because html is the renderer
// every format depends on: the HTML <title>/<meta author>, the browser-rendered
// PDF's title, the pure-Go PDF's info dictionary, and the EPUB's dc:title/creator
// all derive from them. An empty Title falls back to the document's first heading
// (see DocumentTitle); an empty Author omits author metadata.
var (
	Title  string
	Author string
)

// FirstHeading returns the plain text of the document's first heading, or "" if
// there is none.
func FirstHeading(src []byte) string {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).
		Parser().Parse(text.NewReader(src))
	title := ""
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		var b strings.Builder
		_ = ast.Walk(h, func(c ast.Node, e bool) (ast.WalkStatus, error) {
			if e {
				switch t := c.(type) {
				case *ast.Text:
					b.Write(t.Segment.Value(src))
				case *ast.String:
					b.Write(t.Value)
				}
			}
			return ast.WalkContinue, nil
		})
		title = b.String()
		return ast.WalkStop, nil
	})
	return title
}

// DocumentTitle returns Title, or the document's first heading when Title is
// unset (may be ""). Shared by every output format's title metadata.
func DocumentTitle(src []byte) string {
	if Title != "" {
		return Title
	}
	return FirstHeading(src)
}

// highlightStyle is the chroma theme used to color fenced code blocks. "github"
// is light, so it blends with the light HTML document. The pure-Go PDF path
// (internal/converter/pdf) uses the same style for consistent output.
var highlightStyle = styles.Get("github")

// highlightFormatter emits chroma tokens as class-based <span>s. Classes (not
// inline styles) keep the output compact and let -css recolor tokens via the
// cascade. PreventSurroundingPre lets renderFencedCodeBlock keep writing its own
// <pre><code> wrapper and inject only the colored spans inside.
var highlightFormatter = chromahtml.New(
	chromahtml.WithClasses(true),
	chromahtml.PreventSurroundingPre(true),
)

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
	body, css, hasMermaid, err := renderBody(src, false, false)
	if err != nil {
		return nil, err
	}

	// Embed local images into the body before the mermaid library is appended,
	// so the scan never touches that script (which contains <img>-like strings).
	bodyBytes := inlineLocalImages(body, baseDir)

	var out bytes.Buffer
	out.WriteString(docHeadOpen)
	if t := DocumentTitle(src); t != "" {
		out.WriteString("<title>")
		htmlEscaper.WriteString(&out, t)
		out.WriteString("</title>\n")
	}
	if Author != "" {
		out.WriteString(`<meta name="author" content="`)
		htmlEscaper.WriteString(&out, Author)
		out.WriteString("\">\n")
	}
	// Inject the chroma stylesheet only when a block was actually highlighted,
	// before any ExtraCSS so -css can override highlight colors via the cascade.
	if css != "" {
		out.WriteString("<style>\n")
		out.WriteString(css)
		out.WriteString("</style>\n")
	}
	if ExtraCSS != "" {
		out.WriteString("<style>\n")
		// RenderFrom's goldmark instance never sets WithUnsafe, so raw HTML in
		// the markdown body is already escaped by default; -css must not be a
		// backdoor around that. A literal "</style>" here (invalid as CSS)
		// must not be able to close the tag early and inject live markup.
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

// renderBody parses src and renders just the document body (no <html>/<head>
// wrapper). It returns the body markup, the chroma stylesheet for any
// syntax-highlighted code (empty when nothing was highlighted), and whether the
// body contains an enabled mermaid diagram. xhtml selects well-formed XHTML
// output — void elements like <img> and <hr> are self-closed — which the EPUB
// converter needs; the HTML path leaves it off. Images are left as their
// original references; callers embed them (inline data URIs for HTML, packaged
// archive entries for EPUB).
func renderBody(src []byte, xhtml, asSource bool) ([]byte, string, bool, error) {
	dr := &diagramRenderer{asSource: asSource}
	rendererOpts := []renderer.Option{
		renderer.WithNodeRenderers(util.Prioritized(dr, 10)),
	}
	if xhtml {
		rendererOpts = append(rendererOpts, ghtml.WithXHTML())
	}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// Generate GitHub-style id attributes on headings so in-document links
		// like [x](#my-section) resolve to the heading.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(rendererOpts...),
	)

	doc := md.Parser().Parse(text.NewReader(src))
	hasMermaid := containsEnabledDiagram(doc, src, "mermaid")

	var body bytes.Buffer
	if err := md.Renderer().Render(&body, src, doc); err != nil {
		return nil, "", false, err
	}

	var css string
	if dr.highlighted {
		var s bytes.Buffer
		_ = highlightFormatter.WriteCSS(&s, highlightStyle)
		css = s.String()
	}
	return body.Bytes(), css, hasMermaid, nil
}

// ChromaCSS returns the syntax-highlight stylesheet for the named chroma style
// (e.g. "github-dark"), for callers that need a variant beyond the default the
// document uses — the EPUB converter uses it for a prefers-color-scheme:dark
// block. Token class names are the same across styles, so a dark variant scoped
// in a dark media query cleanly overrides the light one.
func ChromaCSS(style string) string {
	var b bytes.Buffer
	_ = highlightFormatter.WriteCSS(&b, styles.Get(style))
	return b.String()
}

// XHTMLBody renders markdown to a well-formed XHTML body fragment plus the
// chroma stylesheet for any highlighted code, for the EPUB converter. Unlike
// RenderFrom it does not wrap the result in a full document and does not inline
// images — the caller packages them into the archive. Enabled diagrams are left
// as <pre class="lang">source</pre> (asSource mode) rather than rendered, so the
// EPUB converter can render a light and a dark variant of each and toggle them
// by the reader's color scheme.
func XHTMLBody(src []byte) ([]byte, string, error) {
	// asSource: leave enabled diagrams as <pre class="lang">source</pre> so the
	// EPUB converter renders its own light and dark variants of each.
	body, css, _, err := renderBody(src, true, true)
	return body, css, err
}

// imgSrcRe matches the src attribute of an <img> tag, capturing the URL.
var imgSrcRe = regexp.MustCompile(`(<img\b[^>]*?\bsrc=")([^"]*)(")`)

// inlineLocalImages rewrites <img src="..."> references that point at local
// files into self-contained data URIs, resolving relative paths against baseDir.
// Already-inlined (data:) sources are left untouched, as is any path that cannot
// be read — a broken local reference is reported but does not fail the render.
// Remote (scheme://) sources are normally left as live references; with -flatten
// (Flatten), http(s) images are fetched and embedded too, for a fully
// self-contained document (a fetch failure leaves the original src in place).
func inlineLocalImages(doc []byte, baseDir string) []byte {
	return imgSrcRe.ReplaceAllFunc(doc, func(m []byte) []byte {
		g := imgSrcRe.FindSubmatch(m)
		src := string(g[2])
		if src == "" || strings.HasPrefix(src, "data:") {
			return m
		}
		if urlref.HasScheme(src) {
			// Remote images stay live references unless -flatten wants a fully
			// self-contained document, in which case fetch + embed.
			if Flatten && (strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")) {
				if uri, ok := fetchRemoteImage(src); ok {
					return append(append(append([]byte(nil), g[1]...), uri...), g[3]...)
				}
			}
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

// RemoteUserAgent is the User-Agent sent when -flatten fetches remote images to
// embed. Browser-like by default because some hosts (CDNs, Wikipedia, etc.) 403
// the default Go client UA; overridable via the -user-agent CLI flag.
var RemoteUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// remoteImageClient fetches remote images for -flatten. The timeout bounds a
// hung host so a single bad reference can't hang the whole conversion.
var remoteImageClient = &http.Client{Timeout: 30 * time.Second}

// fetchRemoteImage downloads an http(s) image and returns it as a data URI. On
// any failure it warns to stderr and returns ok=false so the caller leaves the
// original src in place — a broken reference must not fail the conversion. The
// MIME type comes from the response Content-Type, falling back to the URL's
// extension (remote URLs often lack a useful one).
// ponytail: no response-size cap; the client timeout bounds a hung host. Add a
// MaxBytesReader if a pathologically huge image ever shows up.
func fetchRemoteImage(url string) (string, bool) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: cannot embed remote image %q: %v\n", url, err)
		return "", false
	}
	req.Header.Set("User-Agent", RemoteUserAgent)

	resp, err := remoteImageClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: cannot embed remote image %q: %v\n", url, err)
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "md2: cannot embed remote image %q: HTTP %s\n", url, resp.Status)
		return "", false
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: cannot embed remote image %q: %v\n", url, err)
		return "", false
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = imageMIME(url)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
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

// RequiresBrowser reports whether rendering the source to PDF needs the
// headless browser rather than the pure-Go renderer (goldmark-pdf), because it
// contains an enabled diagram the pure-Go path cannot produce: mermaid needs a
// client-side JS runtime, and d2 renders to inline SVG that gofpdf cannot
// rasterize. Either way the browser draws it faithfully. The PDF renderer uses
// this to choose its path; -flatten uses it to decide a document needs the
// rasterizer.
func RequiresBrowser(src []byte) bool {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).
		Parser().Parse(text.NewReader(src))
	for lang := range enabledDiagrams {
		if containsEnabledDiagram(doc, src, lang) {
			return true
		}
	}
	return false
}

// RequiresMermaidWait reports whether the source contains an enabled mermaid
// diagram, which the browser must draw asynchronously (via the inlined mermaid
// script) before the PDF is printed. It is distinct from RequiresBrowser: a d2
// diagram routes through the browser too, but its SVG is already in the DOM at
// load, so there is nothing to wait for.
func RequiresMermaidWait(src []byte) bool {
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

// diagramRenderer overrides fenced-code-block rendering for enabled diagram
// languages: a `mermaid` block becomes <pre class="mermaid"> (which the mermaid
// library turns into SVG client-side), while a `d2` block is compiled to SVG
// in-process and inlined directly. Every other code block with a known language
// is syntax-highlighted via chroma; unlabeled or unknown-language blocks keep
// goldmark's default <pre><code> output.
type diagramRenderer struct {
	// highlighted records whether any code block was syntax-highlighted, so
	// RenderFrom knows to inline the chroma stylesheet.
	highlighted bool
	// asSource, when set, emits an enabled diagram as <pre class="lang">source</pre>
	// instead of rendering it, so a caller (the EPUB converter) can render its own
	// light and dark variants. Syntax highlighting still applies to normal code.
	asSource bool
}

func (r *diagramRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
}

func (r *diagramRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)

	// In asSource mode, hand an enabled diagram's raw source to the caller as
	// <pre class="lang">source</pre> (mermaid already uses this shape) instead of
	// rendering it, so the EPUB converter can produce light and dark variants.
	if r.asSource {
		if lang := enabledDiagramLang(n, source); lang != "" {
			w.WriteString(`<pre class="`)
			w.WriteString(lang)
			w.WriteString(`">`)
			writeLines(w, source, n)
			w.WriteString("</pre>\n")
			return ast.WalkSkipChildren, nil
		}
	}

	switch enabledDiagramLang(n, source) {
	case "mermaid":
		w.WriteString(`<pre class="mermaid">`)
		writeLines(w, source, n)
		w.WriteString("</pre>\n")
		// With -keep-diagram-source, fall through to also render the source as a
		// code block below (rendered diagram first, source second).
		if !KeepDiagramSource {
			return ast.WalkSkipChildren, nil
		}
	case "d2":
		if svg, err := renderD2(rawLines(source, n), 0); err != nil {
			// A broken diagram must not fail the whole conversion: warn and
			// fall through to render the block as plain code.
			fmt.Fprintf(os.Stderr, "md2: d2 render failed: %v\n", err)
		} else {
			w.WriteString(`<div class="d2">`)
			w.Write(svg)
			w.WriteString("</div>\n")
			if !KeepDiagramSource {
				return ast.WalkSkipChildren, nil
			}
		}
	case "plantuml":
		if svg, err := renderPlantUML(rawLines(source, n)); err != nil {
			// A server/network error must not fail the whole conversion: warn
			// and fall through to render the block as plain code.
			fmt.Fprintf(os.Stderr, "md2: plantuml render failed: %v\n", err)
		} else {
			w.WriteString(`<div class="plantuml">`)
			w.Write(svg)
			w.WriteString("</div>\n")
			if !KeepDiagramSource {
				return ast.WalkSkipChildren, nil
			}
		}
	}

	// A block with a language chroma recognizes is syntax-highlighted. Unknown
	// or unlabeled languages fall through to plain <pre><code>.
	if lang := string(n.Language(source)); lang != "" {
		if lexer := lexers.Get(lang); lexer != nil {
			if r.highlightCode(w, source, n, lang, lexer) {
				return ast.WalkSkipChildren, nil
			}
		}
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

// highlightCode syntax-highlights a code block with chroma, writing
// <pre class="chroma language-<lang>"><code> plus class-based token spans. It
// returns true on success; on any tokenize/format error it warns to stderr and
// returns false so the caller falls back to plain rendering — a lexer glitch
// must never fail the whole conversion.
func (r *diagramRenderer) highlightCode(w util.BufWriter, source []byte, n ast.Node, lang string, lexer chroma.Lexer) bool {
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, string(rawLines(source, n)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "md2: highlight failed for %q: %v\n", lang, err)
		return false
	}
	w.WriteString(`<pre class="chroma"><code class="language-`)
	htmlEscaper.WriteString(w, lang)
	w.WriteString(`">`)
	if err := highlightFormatter.Format(w, highlightStyle, iterator); err != nil {
		// The <pre><code> prefix is already written, but returning false here
		// would double-render; the block is effectively plain on error, which is
		// acceptable and rare. Warn and finish the wrapper.
		fmt.Fprintf(os.Stderr, "md2: highlight failed for %q: %v\n", lang, err)
	}
	w.WriteString("</code></pre>\n")
	r.highlighted = true
	return true
}

// writeLines writes a code block's raw lines, HTML-escaped.
func writeLines(w util.BufWriter, source []byte, n ast.Node) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		htmlEscaper.WriteString(w, string(seg.Value(source)))
	}
}

// rawLines returns a code block's raw, unescaped content — the input a diagram
// compiler needs (writeLines HTML-escapes, which would corrupt the source).
func rawLines(source []byte, n ast.Node) []byte {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(source))
	}
	return buf.Bytes()
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

// BaseCSS is md2's built-in stylesheet (readable body, bordered tables, code
// blocks). It is embedded in the HTML document head and also reused by the EPUB
// converter, which writes it to a packaged stylesheet so ebooks share the same
// base look.
const BaseCSS = `body{font-family:-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;line-height:1.5;margin:2rem;padding:0 1rem;color:#1a1a1a}
h1,h2,h3,h4{line-height:1.25}
table{border-collapse:collapse;width:100%;margin:1rem 0}
th,td{border:1px solid #ccc;padding:.4rem .6rem;text-align:left;vertical-align:top}
th{background:#f2f2f2}
code{background:#f4f4f4;padding:.1rem .3rem;border-radius:3px;font-family:ui-monospace,Menlo,Consolas,monospace}
pre{background:#f4f4f4;padding:1rem;border-radius:6px;overflow:auto}
pre code{background:none;padding:0}
pre.mermaid{background:none;padding:0;text-align:center}
blockquote{border-left:4px solid #ddd;margin:0;padding:.2rem 1rem;color:#555}
img{max-width:100%}`

const docHeadOpen = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<style>
` + BaseCSS + `
</style>
`

// MermaidStandalonePage returns a minimal HTML document rendering a single
// mermaid diagram client-side, for the EPUB converter to load in a headless
// browser and extract the rendered SVG (an ebook reader has no JS runtime, so
// mermaid is pre-rendered). htmlLabels:false makes mermaid emit SVG <text>
// labels rather than <foreignObject> HTML, so the extracted SVG is well-formed
// XML that inlines into the XHTML chapter. The init script signals completion
// via window.__md2MermaidDone, which the caller waits on — the same contract as
// the inlined HTML/PDF path.
func MermaidStandalonePage(source []byte, theme string) []byte {
	// SVG text labels (htmlLabels:false) rather than <foreignObject> HTML, both
	// for well-formed XHTML and because EPUB readers support SVG text far more
	// reliably than foreignObject. For the dark variant use the "base" theme with
	// an explicit dark palette — mermaid's built-in "dark" theme renders too dark
	// on a near-black page and mis-colors SVG-text labels.
	themeOpt := ""
	if theme == "dark" {
		themeOpt = ",theme:'base',themeVariables:{darkMode:true,background:'#0d1117'," +
			"primaryColor:'#21262d',primaryTextColor:'#e6edf3',primaryBorderColor:'#8b949e'," +
			"secondaryColor:'#161b22',tertiaryColor:'#161b22',lineColor:'#8b949e'," +
			"textColor:'#e6edf3',mainBkg:'#21262d',nodeBorder:'#8b949e'}"
	}
	init := "mermaid.initialize({startOnLoad:false,htmlLabels:false," +
		"flowchart:{htmlLabels:false}" + themeOpt + "});\n" +
		"window.__md2Mermaid=mermaid.run()" +
		".then(function(){window.__md2MermaidDone=true;})" +
		".catch(function(e){window.__md2MermaidErr=String(e);window.__md2MermaidDone=true;});"

	var b bytes.Buffer
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">` +
		`<style>pre.mermaid{background:none;padding:0}</style></head><body>` +
		`<pre class="mermaid">`)
	htmlEscaper.WriteString(&b, string(source))
	b.WriteString("</pre>\n<script>")
	// </script> can only appear inside a JS string literal in the minified lib;
	// neutralise it so it cannot close the surrounding <script> element.
	b.WriteString(strings.ReplaceAll(mermaidJS, "</script>", `<\/script>`))
	b.WriteString("</script>\n<script>")
	b.WriteString(init)
	b.WriteString("</script></body></html>")
	return b.Bytes()
}

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
