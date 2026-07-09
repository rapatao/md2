package html

import (
	"bytes"
	"compress/flate"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	out, err := Render([]byte("# Title\n\nbody **bold**\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)

	for _, want := range []string{
		"<!DOCTYPE html>",
		`<meta charset="utf-8">`,
		"<style>",
		`<h1 id="title">Title</h1>`,
		"<strong>bold</strong>",
		"</body>",
		"</html>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Render output missing %q", want)
		}
	}
}

// In-document links ([text](#anchor)) only work when the target heading
// carries a matching id attribute. Auto heading IDs must stay enabled.
func TestRenderHeadingAnchors(t *testing.T) {
	out, err := Render([]byte("[Go](#my-section)\n\n## My Section\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<a href="#my-section">Go</a>`,
		`<h2 id="my-section">My Section</h2>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Render output missing %q:\n%s", want, s)
		}
	}
}

func TestRenderGFMTable(t *testing.T) {
	out, err := Render([]byte("| a | b |\n|---|---|\n| 1 | 2 |\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("<table>")) {
		t.Errorf("GFM table not rendered:\n%s", out)
	}
}

// setExtraCSS sets ExtraCSS for one test and resets it afterward.
func setExtraCSS(t *testing.T, css string) {
	t.Helper()
	ExtraCSS = css
	t.Cleanup(func() { ExtraCSS = "" })
}

func TestRenderNoExtraCSSByDefault(t *testing.T) {
	out, err := Render([]byte("# Title\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := bytes.Count(out, []byte("<style>")); n != 1 {
		t.Errorf("expected exactly 1 <style> block with no -css set, got %d:\n%s", n, out)
	}
}

func TestRenderExtraCSS(t *testing.T) {
	setExtraCSS(t, "body{background:#eef}")
	out, err := Render([]byte("# Title\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"body{background:#eef}",  // extra CSS present
		"pre{background:#f4f4f4", // built-in styling still present (append, not replace)
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Render output missing %q:\n%s", want, s)
		}
	}
}

func TestRenderExtraCSSNeutralizesClosingTag(t *testing.T) {
	setExtraCSS(t, "body{color:red}</style><script>alert(1)</script>")
	out, err := Render([]byte("# Title\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	// A literal </style> in -css must not close the tag early: only the two
	// </style> tags we emit ourselves (built-in block + extra block) may appear.
	if n := strings.Count(s, "</style>"); n != 2 {
		t.Errorf("expected exactly 2 </style> tags, got %d:\n%s", n, s)
	}
	if !strings.Contains(s, "body{color:red}") {
		t.Errorf("Render output missing extra CSS text:\n%s", s)
	}
}

const mermaidDoc = "# Diagram\n\n```mermaid\ngraph TD; A-->B;\n```\n"

// enableMermaid turns mermaid rendering on for one test and resets the global
// enabled set afterwards.
func enableMermaid(t *testing.T) {
	t.Helper()
	if err := EnableDiagrams([]string{"mermaid"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	t.Cleanup(func() { enabledDiagrams = map[string]bool{} })
}

func TestRenderMermaidDisabledByDefault(t *testing.T) {
	out, err := Render([]byte(mermaidDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if strings.Contains(s, `<pre class="mermaid">`) {
		t.Error("mermaid rendered while disabled")
	}
	if strings.Contains(s, "mermaid.run()") {
		t.Error("mermaid script injected while disabled")
	}
	// Falls back to a normal code block.
	if !strings.Contains(s, `<pre><code class="language-mermaid">`) {
		t.Errorf("disabled mermaid not rendered as code block:\n%s", s)
	}
}

func TestRenderMermaidEnabled(t *testing.T) {
	enableMermaid(t)
	out, err := Render([]byte(mermaidDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)

	for _, want := range []string{
		`<pre class="mermaid">graph TD; A--&gt;B;`,
		"mermaid.run()", // init script present
	} {
		if !strings.Contains(s, want) {
			t.Errorf("mermaid output missing %q", want)
		}
	}
	// The inlined library must not contain a raw </script> that would close
	// the element early; the only </script> tags are the two we emit.
	if n := strings.Count(s, "</script>"); n != 2 {
		t.Errorf("expected exactly 2 </script> tags, got %d", n)
	}
}

func TestRenderNoMermaidNoScript(t *testing.T) {
	enableMermaid(t)
	out, err := Render([]byte("just `code` here\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(out), "mermaid.run()") {
		t.Error("mermaid script injected into a document with no diagrams")
	}
}

func TestRequiresBrowser(t *testing.T) {
	if RequiresBrowser([]byte(mermaidDoc)) {
		t.Error("mermaid doc requires browser while disabled")
	}

	enableMermaid(t)
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain text", "no diagrams here\n", false},
		{"mermaid block", mermaidDoc, true},
		{"other code block", "```go\nfmt.Println()\n```\n", false},
		{"inline mention", "use the `mermaid` tool\n", false},
	}
	for _, tt := range tests {
		if got := RequiresBrowser([]byte(tt.in)); got != tt.want {
			t.Errorf("%s: RequiresBrowser = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEnableDiagramsUnknown(t *testing.T) {
	t.Cleanup(func() { enabledDiagrams = map[string]bool{} })
	if err := EnableDiagrams([]string{"graphviz"}); err == nil {
		t.Error("expected error for unknown renderer, got nil")
	}
	if enabledDiagrams["graphviz"] {
		t.Error("unknown renderer was enabled despite error")
	}
}

func TestEnableDiagramsAll(t *testing.T) {
	t.Cleanup(func() { enabledDiagrams = map[string]bool{} })
	if err := EnableDiagrams([]string{"all"}); err != nil {
		t.Fatalf("EnableDiagrams(all): %v", err)
	}
	for _, d := range SupportedDiagrams() {
		if !enabledDiagrams[d] {
			t.Errorf("%q not enabled by \"all\"", d)
		}
	}
}

const d2Doc = "# Diagram\n\n```d2\nx -> y\n```\n"

// enableD2 turns d2 rendering on for one test and resets the global enabled set
// afterwards.
func enableD2(t *testing.T) {
	t.Helper()
	if err := EnableDiagrams([]string{"d2"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	t.Cleanup(func() { enabledDiagrams = map[string]bool{} })
}

func TestRenderD2DisabledByDefault(t *testing.T) {
	out, err := Render([]byte(d2Doc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "<svg") {
		t.Error("d2 rendered while disabled")
	}
	if !strings.Contains(s, `<pre><code class="language-d2">`) {
		t.Errorf("disabled d2 not rendered as code block:\n%s", s)
	}
}

func TestRenderD2Enabled(t *testing.T) {
	enableD2(t)
	out, err := Render([]byte(d2Doc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `<div class="d2">`) {
		t.Errorf("d2 output missing wrapper div:\n%s", s)
	}
	if !strings.Contains(s, "<svg") {
		t.Errorf("d2 output missing inline svg:\n%s", s)
	}
	// d2 renders in-process; no client-side mermaid runtime should be inlined.
	if strings.Contains(s, "mermaid.run()") {
		t.Error("mermaid script injected for a d2-only document")
	}
	// The raw d2 source must not survive as a code block.
	if strings.Contains(s, `class="language-d2"`) {
		t.Errorf("d2 block left as raw code:\n%s", s)
	}
}

// keepDiagramSource turns on -keep-diagram-source for one test and resets the
// global afterwards.
func keepDiagramSource(t *testing.T) {
	t.Helper()
	KeepDiagramSource = true
	t.Cleanup(func() { KeepDiagramSource = false })
}

func TestRenderMermaidKeepSource(t *testing.T) {
	enableMermaid(t)
	keepDiagramSource(t)
	out, err := Render([]byte(mermaidDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)

	rendered := strings.Index(s, `<pre class="mermaid">`)
	if rendered < 0 {
		t.Fatalf("mermaid diagram not rendered:\n%s", s)
	}
	source := strings.Index(s, `class="language-mermaid"`)
	if source < 0 {
		t.Fatalf("mermaid source block not kept:\n%s", s)
	}
	// The rendered diagram must come before the source block.
	if rendered > source {
		t.Errorf("mermaid source appears before the rendered diagram (rendered=%d source=%d)", rendered, source)
	}
}

func TestRenderD2KeepSource(t *testing.T) {
	enableD2(t)
	keepDiagramSource(t)
	out, err := Render([]byte(d2Doc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)

	rendered := strings.Index(s, `<div class="d2">`)
	if rendered < 0 {
		t.Fatalf("d2 diagram not rendered:\n%s", s)
	}
	source := strings.Index(s, `class="language-d2"`)
	if source < 0 {
		t.Fatalf("d2 source block not kept:\n%s", s)
	}
	if rendered > source {
		t.Errorf("d2 source appears before the rendered diagram (rendered=%d source=%d)", rendered, source)
	}
}

func TestRenderMermaidNoKeepSourceByDefault(t *testing.T) {
	enableMermaid(t)
	out, err := Render([]byte(mermaidDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Without the flag, the source must not survive alongside the diagram.
	if strings.Contains(string(out), `class="language-mermaid"`) {
		t.Errorf("mermaid source kept without -keep-diagram-source:\n%s", out)
	}
}

func TestRenderD2InvalidFallsBack(t *testing.T) {
	enableD2(t)
	// Unbalanced braces make the d2 compiler fail; the block must degrade to a
	// plain code block rather than crash or fail the whole render.
	out, err := Render([]byte("```d2\nx -> {\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "<svg") {
		t.Error("invalid d2 unexpectedly produced svg")
	}
	if !strings.Contains(s, `<pre><code class="language-d2">`) {
		t.Errorf("invalid d2 not rendered as code block:\n%s", s)
	}
}

func TestRequiresBrowserD2(t *testing.T) {
	if RequiresBrowser([]byte(d2Doc)) {
		t.Error("d2 doc requires browser while disabled")
	}
	enableD2(t)
	if !RequiresBrowser([]byte(d2Doc)) {
		t.Error("enabled d2 doc should route to the browser for PDF")
	}
	// d2 SVG is inline at load, so it needs no async mermaid wait.
	if RequiresMermaidWait([]byte(d2Doc)) {
		t.Error("d2 doc should not require the mermaid wait")
	}
}

func TestRequiresMermaidWait(t *testing.T) {
	enableMermaid(t)
	if !RequiresMermaidWait([]byte(mermaidDoc)) {
		t.Error("mermaid doc should require the mermaid wait")
	}
	if RequiresMermaidWait([]byte("no diagrams here\n")) {
		t.Error("plain doc should not require the mermaid wait")
	}
}

func TestConverterConvert(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("# Hi\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`<h1 id="hi">Hi</h1>`)) {
		t.Errorf("Convert output missing heading:\n%s", buf.Bytes())
	}
}

// tinyPNG is a valid 1x1 PNG.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func TestInlineLocalImages(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}

	doc := []byte(`<img src="dot.png" alt="x">` +
		`<img src="https://example.com/a.png">` +
		`<img src="data:image/png;base64,AAAA">` +
		`<img src="missing.png">`)

	out := string(inlineLocalImages(doc, dir))

	// Local file becomes a data URI; the original relative ref is gone.
	if !strings.Contains(out, `src="data:image/png;base64,iVBOR`) {
		t.Errorf("local image not inlined:\n%s", out)
	}
	if strings.Contains(out, `src="dot.png"`) {
		t.Errorf("relative ref should be replaced:\n%s", out)
	}
	// Remote, already-inlined, and unreadable refs are left untouched.
	if !strings.Contains(out, `src="https://example.com/a.png"`) {
		t.Errorf("remote ref should be untouched:\n%s", out)
	}
	if !strings.Contains(out, `src="data:image/png;base64,AAAA"`) {
		t.Errorf("existing data URI should be untouched:\n%s", out)
	}
	if !strings.Contains(out, `src="missing.png"`) {
		t.Errorf("unreadable ref should be left as-is:\n%s", out)
	}
}

// With -flatten, remote http(s) images are fetched and embedded as data URIs
// (MIME from the response Content-Type), while non-flatten leaves them live.
func TestInlineLocalImagesFlattenEmbedsRemote(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
	defer srv.Close()

	doc := []byte(`<img src="` + srv.URL + `/a.png">`)

	// Default (no -flatten): remote ref left untouched.
	if out := string(inlineLocalImages(doc, ".")); !strings.Contains(out, `src="`+srv.URL+`/a.png"`) {
		t.Errorf("remote ref should be left live without -flatten:\n%s", out)
	}

	Flatten = true
	defer func() { Flatten = false }()

	out := string(inlineLocalImages(doc, "."))
	if !strings.Contains(out, `src="data:image/png;base64,iVBOR`) {
		t.Errorf("remote image not embedded under -flatten:\n%s", out)
	}
	if strings.Contains(out, srv.URL) {
		t.Errorf("remote URL should be replaced under -flatten:\n%s", out)
	}
	if gotUA != RemoteUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, RemoteUserAgent)
	}

	// -user-agent override is honored.
	old := RemoteUserAgent
	RemoteUserAgent = "custom-agent/1.0"
	defer func() { RemoteUserAgent = old }()
	inlineLocalImages(doc, ".")
	if gotUA != "custom-agent/1.0" {
		t.Errorf("override User-Agent = %q, want %q", gotUA, "custom-agent/1.0")
	}
}

// A remote image that fails to fetch under -flatten leaves the original src in
// place rather than failing the render.
func TestInlineLocalImagesFlattenRemoteFailureKeepsSrc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	Flatten = true
	defer func() { Flatten = false }()

	doc := []byte(`<img src="` + srv.URL + `/missing.png">`)
	out := string(inlineLocalImages(doc, "."))
	if !strings.Contains(out, `src="`+srv.URL+`/missing.png"`) {
		t.Errorf("failed remote fetch should leave src in place:\n%s", out)
	}
}

// RenderFrom embeds a local image referenced from the markdown.
func TestRenderFromEmbedsImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RenderFrom([]byte("![x](pic.png)\n"), dir)
	if err != nil {
		t.Fatalf("RenderFrom: %v", err)
	}
	if !strings.Contains(string(out), `src="data:image/png;base64,iVBOR`) {
		t.Errorf("image not embedded:\n%s", out)
	}
	if strings.Contains(string(out), `src="pic.png"`) {
		t.Errorf("relative ref should be gone:\n%s", out)
	}
}

func TestImageMIME(t *testing.T) {
	cases := map[string]string{
		"a.png": "image/png", "a.JPG": "image/jpeg", "a.jpeg": "image/jpeg",
		"a.gif": "image/gif", "a.svg": "image/svg+xml", "a.webp": "image/webp",
		"a.unknown": "image/png",
	}
	for path, want := range cases {
		if got := imageMIME(path); got != want {
			t.Errorf("imageMIME(%q) = %q, want %q", path, got, want)
		}
	}
}

// A fenced block tagged with a chroma-known language is syntax-highlighted:
// token spans plus a single inlined chroma stylesheet.
func TestRenderHighlightsKnownLanguage(t *testing.T) {
	out, err := Render([]byte("```go\nfunc main() {}\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`<pre class="chroma"><code class="language-go"><span`, // highlighted wrapper + token spans
		".chroma {", // generated highlight stylesheet inlined
	} {
		if !strings.Contains(s, want) {
			t.Errorf("highlighted output missing %q:\n%s", want, s)
		}
	}
}

// An unlabeled fenced block is not highlighted and no chroma stylesheet is
// emitted.
func TestRenderNoLanguageStaysPlain(t *testing.T) {
	out, err := Render([]byte("```\nplain text\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "<pre><code>") {
		t.Errorf("unlabeled block should stay plain <pre><code>:\n%s", s)
	}
	if strings.Contains(s, "chroma") {
		t.Errorf("unlabeled block must not emit chroma output:\n%s", s)
	}
	if n := strings.Count(s, "<style>"); n != 1 {
		t.Errorf("expected exactly 1 <style> block (built-in only), got %d:\n%s", n, s)
	}
}

// A block tagged with a language chroma does not know stays plain, keeping its
// language- class, with no highlight stylesheet.
func TestRenderUnknownLanguageStaysPlain(t *testing.T) {
	out, err := Render([]byte("```notalang\nsome code\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `<pre><code class="language-notalang">`) {
		t.Errorf("unknown language should stay plain with language class:\n%s", s)
	}
	if strings.Contains(s, "chroma") {
		t.Errorf("unknown language must not emit chroma output:\n%s", s)
	}
}

// -css is inlined after the highlight stylesheet so it can override token
// colors via the cascade.
func TestRenderHighlightCSSBeforeExtraCSS(t *testing.T) {
	setExtraCSS(t, ".chroma .k{color:hotpink}")
	out, err := Render([]byte("```go\nfunc main() {}\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	hi := strings.Index(s, ".chroma {")
	extra := strings.Index(s, ".chroma .k{color:hotpink}")
	if hi < 0 || extra < 0 {
		t.Fatalf("expected both highlight and extra CSS present:\n%s", s)
	}
	if extra < hi {
		t.Errorf("extra CSS must come after highlight stylesheet (cascade), got extra=%d highlight=%d", extra, hi)
	}
}

const plantumlDoc = "# Diagram\n\n```plantuml\n@startuml\nAlice -> Bob: hi\n@enduml\n```\n"

// enablePlantUML turns plantuml rendering on for one test and resets the global
// enabled set afterwards.
func enablePlantUML(t *testing.T) {
	t.Helper()
	if err := EnableDiagrams([]string{"plantuml"}); err != nil {
		t.Fatalf("EnableDiagrams: %v", err)
	}
	t.Cleanup(func() { enabledDiagrams = map[string]bool{} })
}

// setPlantUMLServer points the renderer at url for one test and restores the
// default afterwards.
func setPlantUMLServer(t *testing.T, url string) {
	t.Helper()
	prev := PlantUMLServer
	PlantUMLServer = url
	t.Cleanup(func() { PlantUMLServer = prev })
}

func TestRenderPlantUMLDisabledByDefault(t *testing.T) {
	out, err := Render([]byte(plantumlDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if strings.Contains(s, `<div class="plantuml">`) {
		t.Error("plantuml rendered while disabled")
	}
	if !strings.Contains(s, `<pre><code class="language-plantuml">`) {
		t.Errorf("disabled plantuml not rendered as code block:\n%s", s)
	}
}

func TestRenderPlantUMLEnabled(t *testing.T) {
	enablePlantUML(t)

	const svg = `<svg xmlns="http://www.w3.org/2000/svg"><text>diagram</text></svg>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/svg/") {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, `<?xml version="1.0"?>`+svg)
	}))
	defer srv.Close()
	setPlantUMLServer(t, srv.URL)

	out, err := Render([]byte(plantumlDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `<div class="plantuml">`) {
		t.Errorf("plantuml output missing wrapper div:\n%s", s)
	}
	if !strings.Contains(s, svg) {
		t.Errorf("plantuml output missing fetched svg:\n%s", s)
	}
	if strings.Contains(s, "<?xml") {
		t.Errorf("xml prolog not stripped from inlined svg:\n%s", s)
	}
	if strings.Contains(s, `class="language-plantuml"`) {
		t.Errorf("plantuml block left as raw code:\n%s", s)
	}
}

func TestRenderPlantUMLServerErrorFallsBack(t *testing.T) {
	enablePlantUML(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	setPlantUMLServer(t, srv.URL)

	out, err := Render([]byte(plantumlDoc))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if strings.Contains(s, `<div class="plantuml">`) {
		t.Error("server error unexpectedly produced a plantuml div")
	}
	if !strings.Contains(s, `<pre><code class="language-plantuml">`) {
		t.Errorf("failed plantuml not rendered as code block:\n%s", s)
	}
}

func TestRequiresBrowserPlantUML(t *testing.T) {
	if RequiresBrowser([]byte(plantumlDoc)) {
		t.Error("plantuml doc requires browser while disabled")
	}
	enablePlantUML(t)
	if !RequiresBrowser([]byte(plantumlDoc)) {
		t.Error("enabled plantuml doc should route to the browser for PDF")
	}
	// plantuml SVG is inline at load, so it needs no async mermaid wait.
	if RequiresMermaidWait([]byte(plantumlDoc)) {
		t.Error("plantuml doc should not require a mermaid wait")
	}
}

func TestPlantumlEncode(t *testing.T) {
	src := []byte("@startuml\nAlice -> Bob: hi\n@enduml")
	enc := plantumlEncode(src)
	if enc == "" {
		t.Fatal("empty encoding")
	}

	deflated := decode64(t, enc)
	got, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflated)))
	if err != nil {
		t.Fatalf("inflate: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("round-trip mismatch: got %q want %q", got, src)
	}
}

// decode64 reverses encode64 for the round-trip test.
func decode64(t *testing.T, s string) []byte {
	t.Helper()
	idx := func(c byte) int64 {
		return int64(strings.IndexByte(plantumlAlphabet, c))
	}
	var out []byte
	for i := 0; i < len(s); i += 4 {
		var v int64
		for j := 0; j < 4; j++ {
			v = v << 6
			if i+j < len(s) {
				v |= idx(s[i+j])
			}
		}
		out = append(out, byte(v>>16), byte(v>>8), byte(v))
	}
	// Trailing zero-padding bytes may appear; the flate reader stops at the
	// stream end, so no trimming is needed here.
	return out
}
