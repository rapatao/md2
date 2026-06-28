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

func TestConverterConvert(t *testing.T) {
	var buf bytes.Buffer
	if err := (Converter{}).Convert([]byte("# Hi\n"), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("<h1>Hi</h1>")) {
		t.Errorf("Convert output missing heading:\n%s", buf.Bytes())
	}
}
