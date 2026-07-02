package cli

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// run invokes Run with a fixed test version and discards -stdout output; use
// Run directly when a test cares about either.
func run(args []string) error {
	return Run(args, "test", io.Discard)
}

func TestParseList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"pdf", []string{"pdf"}},
		{"pdf,html", []string{"pdf", "html"}},
		{" pdf , html ", []string{"pdf", "html"}},
		{"pdf,pdf,html,pdf", []string{"pdf", "html"}}, // dedup, order kept
		{"", nil},
		{",, ,", nil},
	}
	for _, tt := range tests {
		if got := parseList(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseList(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// writeInput creates a markdown file in a temp dir and returns its path.
func writeInput(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n\nbody **bold**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunDefaultPDF(t *testing.T) {
	in := writeInput(t)
	if err := run([]string{in}); err != nil {
		t.Fatalf("run: %v", err)
	}

	out := replaceExt(in, ".pdf")
	data := readFile(t, out)
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Errorf("output %s is not a PDF (prefix %q)", out, data[:min(4, len(data))])
	}
}

func TestRunExplicitHTML(t *testing.T) {
	in := writeInput(t)
	if err := run([]string{"-f", "html", in}); err != nil {
		t.Fatalf("run: %v", err)
	}

	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("<h1")) {
		t.Errorf("html output missing <h1>: %q", data)
	}
}

func TestRunHTMLTable(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "t.md")
	md := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	if err := os.WriteFile(in, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("<table>")) {
		t.Errorf("GFM table not rendered: %q", data)
	}
}

func TestRunPDFTableFirst(t *testing.T) {
	// A table as the first block used to panic goldmark-pdf (no font set).
	dir := t.TempDir()
	in := filepath.Join(dir, "t.md")
	md := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	if err := os.WriteFile(in, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "pdf", in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".pdf"))
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Errorf("output is not a PDF: %q", data[:min(4, len(data))])
	}
}

func TestRunMultipleFormats(t *testing.T) {
	in := writeInput(t)
	if err := run([]string{"-f", "pdf,html", in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, ext := range []string{".pdf", ".html"} {
		out := replaceExt(in, ext)
		if _, err := os.Stat(out); err != nil {
			t.Errorf("expected output %s: %v", out, err)
		}
	}
}

// Multiple inputs are concatenated in order into a single document.
func TestRunMergeMultipleInputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	if err := os.WriteFile(a, []byte("# First\n\nfirst body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("# Second\n\nsecond body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", a, b}); err != nil {
		t.Fatalf("run: %v", err)
	}

	// -o is omitted, so the merged output takes the first input's basename.
	data := readFile(t, replaceExt(a, ".html"))
	if !bytes.Contains(data, []byte("First")) || !bytes.Contains(data, []byte("Second")) {
		t.Errorf("merged output missing content from both inputs: %q", data)
	}
	firstIdx := bytes.Index(data, []byte("First"))
	secondIdx := bytes.Index(data, []byte("Second"))
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Errorf("merged output not in input order: %q", data)
	}
}

// Each merged file's relative image references resolve against its own
// directory, not the first file's.
func TestRunMergeResolvesImagesPerFile(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	aDir := filepath.Join(dir, "a")
	bDir := filepath.Join(dir, "b")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aDir, "fig.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "fig.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(aDir, "doc.md")
	b := filepath.Join(bDir, "doc.md")
	if err := os.WriteFile(a, []byte("# A\n\n![a](fig.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("# B\n\n![b](fig.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"-f", "html", a, b}); err != nil {
		t.Fatalf("run: %v", err)
	}

	data := readFile(t, replaceExt(a, ".html"))
	if n := bytes.Count(data, []byte("data:image/png;base64,")); n != 2 {
		t.Errorf("expected both images embedded (2 data URIs), got %d: %q", n, data)
	}
	if bytes.Contains(data, []byte(`src="fig.png"`)) {
		t.Errorf("an image was not resolved and embedded: %q", data)
	}
}

// An unknown format must fail fast, before any output is written.
func TestRunUnknownFormat(t *testing.T) {
	in := writeInput(t)
	err := run([]string{"-f", "html,bogus", in})
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	// Conversion is resolved up front, so nothing should have been written.
	if _, statErr := os.Stat(replaceExt(in, ".html")); statErr == nil {
		t.Error("no file should be written when a format is unknown")
	}
}

// -flatten without diagrams is a no-op that needs no browser, exercising the
// flatten code path in CI.
func TestRunFlattenNoDiagrams(t *testing.T) {
	in := writeInput(t)
	if err := run([]string{"-f", "html", "-flatten", in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("<h1")) {
		t.Errorf("expected html output")
	}
}

func TestRunCSS(t *testing.T) {
	in := writeInput(t)
	cssPath := filepath.Join(filepath.Dir(in), "extra.css")
	if err := os.WriteFile(cssPath, []byte("body{background:#eef}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", "-css", cssPath, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("body{background:#eef}")) {
		t.Errorf("expected -css content in output:\n%s", data)
	}
}

func TestRunCSSLocalImport(t *testing.T) {
	in := writeInput(t)
	dir := filepath.Dir(in)
	if err := os.WriteFile(filepath.Join(dir, "base.css"), []byte("body{color:blue}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cssPath := filepath.Join(dir, "extra.css")
	if err := os.WriteFile(cssPath, []byte(`@import "base.css"; h1{color:red}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", "-css", cssPath, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("body{color:blue}")) {
		t.Errorf("expected imported CSS inlined in output:\n%s", data)
	}
	if !bytes.Contains(data, []byte("h1{color:red}")) {
		t.Errorf("expected importing CSS preserved in output:\n%s", data)
	}
	if bytes.Contains(data, []byte(`@import`)) {
		t.Errorf("local @import should be inlined, not left in output:\n%s", data)
	}
}

func TestRunCSSNestedImport(t *testing.T) {
	in := writeInput(t)
	dir := filepath.Dir(in)
	if err := os.WriteFile(filepath.Join(dir, "grandchild.css"), []byte("a{color:green}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.css"), []byte(`@import url(grandchild.css);`), 0o644); err != nil {
		t.Fatal(err)
	}
	cssPath := filepath.Join(dir, "extra.css")
	if err := os.WriteFile(cssPath, []byte(`@import "child.css";`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", "-css", cssPath, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("a{color:green}")) {
		t.Errorf("expected nested import resolved in output:\n%s", data)
	}
}

func TestRunCSSImportCycle(t *testing.T) {
	in := writeInput(t)
	dir := filepath.Dir(in)
	if err := os.WriteFile(filepath.Join(dir, "a.css"), []byte(`@import "b.css"; a{color:red}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.css"), []byte(`@import "a.css"; b{color:blue}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cssPath := filepath.Join(dir, "a.css")
	// A cycle must not hang or crash Run; the repeat import is simply dropped.
	if err := run([]string{"-f", "html", "-css", cssPath, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("a{color:red}")) || !bytes.Contains(data, []byte("b{color:blue}")) {
		t.Errorf("expected both cyclic files' rules present exactly once:\n%s", data)
	}
}

func TestRunCSSRemoteImportUntouched(t *testing.T) {
	in := writeInput(t)
	cssPath := filepath.Join(filepath.Dir(in), "extra.css")
	if err := os.WriteFile(cssPath, []byte(`@import url(https://example.com/base.css); h1{color:red}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-f", "html", "-css", cssPath, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte(`@import url(https://example.com/base.css);`)) {
		t.Errorf("expected remote @import left untouched for the browser to fetch:\n%s", data)
	}
}

func TestRunCSSMissingFile(t *testing.T) {
	in := writeInput(t)
	err := run([]string{"-f", "html", "-css", filepath.Join(filepath.Dir(in), "nonexistent.css"), in})
	if err == nil {
		t.Fatal("expected error for missing -css file, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("reading -css file")) {
		t.Errorf("expected error to mention 'reading -css file', got: %v", err)
	}
}

func TestRunFormatFromOutputExt(t *testing.T) {
	in := writeInput(t)
	out := filepath.Join(filepath.Dir(in), "custom.html")
	if err := run([]string{"-o", out, in}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, out)
	if !bytes.Contains(data, []byte("<h1")) {
		t.Errorf("expected html in %s", out)
	}
}

func TestRunStdout(t *testing.T) {
	in := writeInput(t)
	var buf bytes.Buffer
	if err := Run([]string{"-f", "html", "-stdout", in}, "test", &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("<h1")) {
		t.Errorf("stdout missing <h1>: %q", buf.Bytes())
	}
	// -stdout without -o must not write a file.
	if _, err := os.Stat(replaceExt(in, ".html")); err == nil {
		t.Error("no file should be written with -stdout and no -o")
	}
}

func TestRunStdoutWithOutput(t *testing.T) {
	in := writeInput(t)
	out := filepath.Join(filepath.Dir(in), "custom.html")
	var buf bytes.Buffer
	if err := Run([]string{"-o", out, "-stdout", in}, "test", &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("<h1")) {
		t.Errorf("stdout missing <h1>: %q", buf.Bytes())
	}
	// -o alongside -stdout still writes the file, with identical content.
	data := readFile(t, out)
	if !bytes.Equal(data, buf.Bytes()) {
		t.Errorf("file and stdout differ:\nfile=%q\nstdout=%q", data, buf.Bytes())
	}
}

func TestRunErrors(t *testing.T) {
	in := writeInput(t)
	tests := []struct {
		name string
		args []string
	}{
		{"output conflicts with multiple formats", []string{"-o", "x.pdf", "-f", "pdf,html", in}},
		{"stdout conflicts with multiple formats", []string{"-stdout", "-f", "pdf,html", in}},
		{"unsupported format", []string{"-f", "docx", in}},
		{"no input", []string{}},
		{"missing input file", []string{filepath.Join(t.TempDir(), "nope.md")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(tt.args); err == nil {
				t.Errorf("run(%v): expected error, got nil", tt.args)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"-version"}, "1.2.3", &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func replaceExt(path, ext string) string {
	return path[:len(path)-len(filepath.Ext(path))] + ext
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatalf("output %s is empty", path)
	}
	return data
}
