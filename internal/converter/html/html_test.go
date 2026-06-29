package html

import (
	"bytes"
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
		"<h1>Title</h1>",
		"<strong>bold</strong>",
		"</body>",
		"</html>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Render output missing %q", want)
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
	if !bytes.Contains(buf.Bytes(), []byte("<h1>Hi</h1>")) {
		t.Errorf("Convert output missing heading:\n%s", buf.Bytes())
	}
}
