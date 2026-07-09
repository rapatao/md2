package text

import (
	"bytes"
	"strings"
	"testing"
)

func render(t *testing.T, md string) string {
	t.Helper()
	return string(Render([]byte(md)))
}

func TestRenderStripsInlineMarkup(t *testing.T) {
	got := render(t, "Para with **bold**, *italic* and `code`.\n")
	want := "Para with bold, italic and code.\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderHeading(t *testing.T) {
	got := render(t, "# Title\n\nbody\n")
	if !strings.HasPrefix(got, "Title\n\n") {
		t.Errorf("heading not rendered as plain line: %q", got)
	}
}

func TestRenderLinkKeepsURL(t *testing.T) {
	got := render(t, "see [docs](https://example.com)\n")
	if !strings.Contains(got, "docs (https://example.com)") {
		t.Errorf("link URL missing: %q", got)
	}
}

func TestRenderLinkSameTextAndURLNotDuplicated(t *testing.T) {
	got := render(t, "[https://example.com](https://example.com)\n")
	if strings.Count(got, "https://example.com") != 1 {
		t.Errorf("URL should appear once: %q", got)
	}
}

func TestRenderUnorderedList(t *testing.T) {
	got := render(t, "- one\n- two\n")
	if !strings.Contains(got, "- one") || !strings.Contains(got, "- two") {
		t.Errorf("unordered markers missing: %q", got)
	}
}

func TestRenderOrderedList(t *testing.T) {
	got := render(t, "1. first\n2. second\n")
	if !strings.Contains(got, "1. first") || !strings.Contains(got, "2. second") {
		t.Errorf("ordered markers missing: %q", got)
	}
}

func TestRenderNestedListIndented(t *testing.T) {
	got := render(t, "- top\n  - child\n")
	if !strings.Contains(got, "\n  - child") {
		t.Errorf("nested item not indented: %q", got)
	}
}

func TestRenderTaskListCheckboxes(t *testing.T) {
	got := render(t, "- [ ] todo one\n- [x] done one\n")
	if !strings.Contains(got, "- [ ] todo one") {
		t.Errorf("unchecked box missing: %q", got)
	}
	if !strings.Contains(got, "- [x] done one") {
		t.Errorf("checked box missing: %q", got)
	}
}

func TestRenderBlockquotePrefix(t *testing.T) {
	got := render(t, "> quoted\n")
	if !strings.Contains(got, "> quoted") {
		t.Errorf("blockquote prefix missing: %q", got)
	}
}

func TestRenderCodeBlockVerbatim(t *testing.T) {
	got := render(t, "```go\nfmt.Println(\"hi\")\n```\n")
	if !strings.Contains(got, `fmt.Println("hi")`) {
		t.Errorf("code block not preserved: %q", got)
	}
}

func TestRenderTableCells(t *testing.T) {
	got := render(t, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(got, "a | b") || !strings.Contains(got, "1 | 2") {
		t.Errorf("table cells not rendered: %q", got)
	}
	// A dashed separator sits between the header and body rows.
	if !strings.Contains(got, "a | b\n-----\n1 | 2") {
		t.Errorf("header separator missing: %q", got)
	}
}

func TestRenderEndsWithSingleNewline(t *testing.T) {
	got := render(t, "# Title\n\nbody\n")
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("output should end with exactly one newline: %q", got)
	}
}

func TestRenderEmpty(t *testing.T) {
	if got := render(t, ""); got != "" {
		t.Errorf("empty input should yield empty output, got %q", got)
	}
}

func TestConverterConvert(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("**hi**\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if buf.String() != "hi\n" {
		t.Errorf("got %q, want %q", buf.String(), "hi\n")
	}
}
