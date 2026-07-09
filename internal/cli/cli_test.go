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
	return Run(args, "test", bytes.NewReader(nil), io.Discard)
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
	out := filepath.Join(dir, "merged.html")
	if err := run([]string{"-f", "html", "-o", out, a, b}); err != nil {
		t.Fatalf("run: %v", err)
	}

	data := readFile(t, out)
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

	out := filepath.Join(dir, "merged.html")
	if err := run([]string{"-f", "html", "-o", out, a, b}); err != nil {
		t.Fatalf("run: %v", err)
	}

	data := readFile(t, out)
	if n := bytes.Count(data, []byte("data:image/png;base64,")); n != 2 {
		t.Errorf("expected both images embedded (2 data URIs), got %d: %q", n, data)
	}
	if bytes.Contains(data, []byte(`src="fig.png"`)) {
		t.Errorf("an image was not resolved and embedded: %q", data)
	}
}

// writeMD writes markdown to path, creating parent dirs.
func writeMD(t *testing.T, path, md string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A directory input merges its .md files, in name order, into one document.
func TestRunDirectoryMerge(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, filepath.Join(dir, "01.md"), "# First\n")
	writeMD(t, filepath.Join(dir, "02.md"), "# Second\n")

	out := filepath.Join(dir, "all.html")
	if err := run([]string{"-f", "html", "-o", out, dir}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, out)
	firstIdx := bytes.Index(data, []byte("First"))
	secondIdx := bytes.Index(data, []byte("Second"))
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Errorf("directory not merged in name order: %q", data)
	}
}

// A directory with -per-file converts each .md to its own output.
func TestRunDirectoryPerFile(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, filepath.Join(dir, "01.md"), "# First\n")
	writeMD(t, filepath.Join(dir, "02.md"), "# Second\n")

	if err := run([]string{"-f", "html", "-per-file", dir}); err != nil {
		t.Fatalf("run: %v", err)
	}
	one := readFile(t, filepath.Join(dir, "01.html"))
	two := readFile(t, filepath.Join(dir, "02.html"))
	if !bytes.Contains(one, []byte("First")) || bytes.Contains(one, []byte("Second")) {
		t.Errorf("01.html should hold only its own content: %q", one)
	}
	if !bytes.Contains(two, []byte("Second")) || bytes.Contains(two, []byte("First")) {
		t.Errorf("02.html should hold only its own content: %q", two)
	}
}

// -per-file applies to explicit multiple files too, one output each.
func TestRunPerFileMultipleInputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	writeMD(t, a, "# A\n")
	writeMD(t, b, "# B\n")

	if err := run([]string{"-per-file", "-f", "html", a, b}); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, p := range []string{replaceExt(a, ".html"), replaceExt(b, ".html")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected per-file output %s: %v", p, err)
		}
	}
}

// -recursive picks up sub-directory files, ordered folder by folder: a folder's
// own files before its sub-folders.
func TestRunDirectoryRecursiveOrder(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, filepath.Join(dir, "02.md"), "# Two\n")
	writeMD(t, filepath.Join(dir, "01.md"), "# One\n")
	writeMD(t, filepath.Join(dir, "sub", "03.md"), "# Three\n")

	out := filepath.Join(dir, "rec.html")
	if err := run([]string{"-f", "html", "-recursive", "-o", out, dir}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, out)
	i1 := bytes.Index(data, []byte("One"))
	i2 := bytes.Index(data, []byte("Two"))
	i3 := bytes.Index(data, []byte("Three"))
	if i1 == -1 || i2 == -1 || i3 == -1 || !(i1 < i2 && i2 < i3) {
		t.Errorf("recursive order want One<Two<Three (folder by folder): %q", data)
	}
}

// Without -recursive, sub-directory files are ignored.
func TestRunDirectoryNonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, filepath.Join(dir, "01.md"), "# Top\n")
	writeMD(t, filepath.Join(dir, "sub", "02.md"), "# Nested\n")

	out := filepath.Join(dir, "all.html")
	if err := run([]string{"-f", "html", "-o", out, dir}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data := readFile(t, out)
	if bytes.Contains(data, []byte("Nested")) {
		t.Errorf("sub-directory file should be skipped without -recursive: %q", data)
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
	if err := Run([]string{"-f", "html", "-stdout", in}, "test", bytes.NewReader(nil), &buf); err != nil {
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
	if err := Run([]string{"-o", out, "-stdout", in}, "test", bytes.NewReader(nil), &buf); err != nil {
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
		{"merge multiple inputs without -o", []string{"-f", "html", in, writeInput(t)}},
		{"per-file with -o", []string{"-per-file", "-o", "x.html", in}},
		{"per-file with -stdout", []string{"-per-file", "-stdout", in}},
		{"empty directory", []string{t.TempDir()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(tt.args); err == nil {
				t.Errorf("run(%v): expected error, got nil", tt.args)
			}
		})
	}
}

func TestRunStdinToStdout(t *testing.T) {
	var buf bytes.Buffer
	stdin := bytes.NewReader([]byte("# Piped\n"))
	if err := Run([]string{"-f", "html", "-stdout", "-"}, "test", stdin, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("<h1")) {
		t.Errorf("stdout missing <h1>: %q", buf.Bytes())
	}
}

func TestRunStdinToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.html")
	stdin := bytes.NewReader([]byte("# Piped\n"))
	if err := Run([]string{"-o", out, "-"}, "test", stdin, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Contains(readFile(t, out), []byte("<h1")) {
		t.Errorf("expected html in %s", out)
	}
}

func TestRunStdinRequiresOutput(t *testing.T) {
	// Reading from stdin with no -o/-stdout has no basename to name the file.
	if err := Run([]string{"-f", "html", "-"}, "test", bytes.NewReader(nil), io.Discard); err == nil {
		t.Error("expected error for stdin input without -o or -stdout")
	}
}

func TestRunVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"-version"}, "1.2.3", bytes.NewReader(nil), &buf); err != nil {
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
