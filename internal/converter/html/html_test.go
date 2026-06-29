package html

import (
	"bytes"
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
	if err := EnableDiagrams([]string{"plantuml"}); err == nil {
		t.Error("expected error for unknown renderer, got nil")
	}
	if enabledDiagrams["plantuml"] {
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
