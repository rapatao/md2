package cli

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
	if err := Run([]string{in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := replaceExt(in, ".pdf")
	data := readFile(t, out)
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Errorf("output %s is not a PDF (prefix %q)", out, data[:min(4, len(data))])
	}
}

func TestRunExplicitHTML(t *testing.T) {
	in := writeInput(t)
	if err := Run([]string{"-f", "html", in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
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
	if err := Run([]string{"-f", "html", in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
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
	if err := Run([]string{"-f", "pdf", in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".pdf"))
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Errorf("output is not a PDF: %q", data[:min(4, len(data))])
	}
}

func TestRunMultipleFormats(t *testing.T) {
	in := writeInput(t)
	if err := Run([]string{"-f", "pdf,html", in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
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
	if err := Run([]string{"-f", "html", a, b}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
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

	if err := Run([]string{"-f", "html", a, b}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
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
	err := Run([]string{"-f", "html,bogus", in}, "test")
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
	if err := Run([]string{"-f", "html", "-flatten", in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data := readFile(t, replaceExt(in, ".html"))
	if !bytes.Contains(data, []byte("<h1")) {
		t.Errorf("expected html output")
	}
}

func TestRunFormatFromOutputExt(t *testing.T) {
	in := writeInput(t)
	out := filepath.Join(filepath.Dir(in), "custom.html")
	if err := Run([]string{"-o", out, in}, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data := readFile(t, out)
	if !bytes.Contains(data, []byte("<h1")) {
		t.Errorf("expected html in %s", out)
	}
}

// withStdout swaps the package stdout writer for a buffer and restores it.
func withStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := stdoutWriter
	buf := &bytes.Buffer{}
	stdoutWriter = buf
	t.Cleanup(func() { stdoutWriter = prev })
	return buf
}

func TestRunStdout(t *testing.T) {
	in := writeInput(t)
	buf := withStdout(t)
	if err := Run([]string{"-f", "html", "-stdout", in}, "test"); err != nil {
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
	buf := withStdout(t)
	if err := Run([]string{"-o", out, "-stdout", in}, "test"); err != nil {
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
			if err := Run(tt.args, "test"); err == nil {
				t.Errorf("Run(%v): expected error, got nil", tt.args)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	if err := Run([]string{"-version"}, "1.2.3"); err != nil {
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
