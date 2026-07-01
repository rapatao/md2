package pdf

import (
	"bytes"
	"testing"
)

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
	if err := renderPureGo([]byte("# Title\n\nbody\n"), &buf); err != nil {
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
	if err := renderPureGo([]byte(md), &buf); err != nil {
		t.Fatalf("renderPureGo: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Error("table-first document did not render to PDF")
	}
}
