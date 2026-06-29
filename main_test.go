package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestRunErrors(t *testing.T) {
	in := writeInput(t)
	tests := []struct {
		name string
		args []string
	}{
		{"output conflicts with multiple formats", []string{"-o", "x.pdf", "-f", "pdf,html", in}},
		{"unsupported format", []string{"-f", "docx", in}},
		{"no input", []string{}},
		{"too many inputs", []string{in, in}},
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

func TestReadConsent(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{" y \n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"maybe\n", false},
		{"", false}, // EOF, no input
	}
	for _, tt := range tests {
		got, err := readConsent(strings.NewReader(tt.in))
		if err != nil {
			t.Errorf("readConsent(%q): unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("readConsent(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestConsentFunc(t *testing.T) {
	// -allow-download authorizes unconditionally.
	if ok, err := consentFunc(true)(); err != nil || !ok {
		t.Errorf("consentFunc(true) = (%v, %v), want (true, nil)", ok, err)
	}
	// Without the flag and without an interactive terminal (the test
	// environment), it must decline rather than block on a prompt.
	if ok, err := consentFunc(false)(); err != nil || ok {
		t.Errorf("consentFunc(false) non-interactive = (%v, %v), want (false, nil)", ok, err)
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
