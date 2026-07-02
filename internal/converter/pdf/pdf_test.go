package pdf

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// tiny1x1PNG is a minimal valid 1x1 transparent PNG, base64-encoded.
const tiny1x1PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestHasNonBMP(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"ascii", "hello world", false},
		{"latin accents", "Configuração à ré", false},
		{"bmp symbols", "€ — → ·", false},
		{"astral emoji", "status 🟢 ok", true},
		{"mixed emoji", "🟢🟡🔴 x", true},
		{"emoji amid text", "a🔴b", true},
	}
	for _, tt := range tests {
		if got := hasNonBMP([]byte(tt.in)); got != tt.want {
			t.Errorf("%s: hasNonBMP(%q) = %v, want %v", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestRenderPureGoSimple(t *testing.T) {
	var buf bytes.Buffer
	if err := renderPureGo([]byte("# Title\n\nbody\n"), "", &buf); err != nil {
		t.Fatalf("renderPureGo: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Errorf("output is not a PDF: %q", buf.Bytes()[:min(4, buf.Len())])
	}
}

func TestRenderPureGoTableFirst(t *testing.T) {
	// A table as the first block previously panicked; it must now render.
	md := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	var buf bytes.Buffer
	if err := renderPureGo([]byte(md), "", &buf); err != nil {
		t.Fatalf("renderPureGo: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Error("table-first document did not render to PDF")
	}
}

func TestRenderPureGoRelativeImageResolvesAgainstSrcDir(t *testing.T) {
	dir := t.TempDir()
	png, err := base64.StdEncoding.DecodeString(tiny1x1PNG)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fig.png"), png, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Run from outside dir, as md2 would when invoked with a path to a file
	// elsewhere; only srcPath (not the CWD) should determine image lookup.
	md := "# Doc\n\n![x](fig.png)\n"
	var buf bytes.Buffer
	if err := renderPureGo([]byte(md), filepath.Join(dir, "doc.md"), &buf); err != nil {
		t.Fatalf("renderPureGo: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Error("document with relative image did not render to PDF")
	}
	// goldmark-pdf logs a missing-image error but still returns a (blank)
	// PDF, so a bare success check isn't enough; confirm the image actually
	// got embedded as an XObject.
	if !bytes.Contains(buf.Bytes(), []byte("/Subtype /Image")) {
		t.Error("image was not embedded in the PDF")
	}
}

func TestRenderPureGoAbsoluteImageResolves(t *testing.T) {
	// Simulates the multi-input merge case: merge.Inputs rewrites each
	// file's relative image references to absolute paths (since the merged
	// document has no single directory to resolve them against), so the
	// pure-Go renderer must be able to look those up too.
	dir := t.TempDir()
	png, err := base64.StdEncoding.DecodeString(tiny1x1PNG)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	imgPath := filepath.Join(dir, "fig.png")
	if err := os.WriteFile(imgPath, png, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	md := "# Doc\n\n![x](" + filepath.ToSlash(imgPath) + ")\n"
	var buf bytes.Buffer
	if err := renderPureGo([]byte(md), filepath.Join(dir, "doc.md"), &buf); err != nil {
		t.Fatalf("renderPureGo: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("/Subtype /Image")) {
		t.Error("image was not embedded in the PDF")
	}
}
