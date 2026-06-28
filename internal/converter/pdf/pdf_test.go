package pdf

import (
	"bytes"
	"testing"
)

func TestStripNonBMP(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ascii unchanged", "hello world", "hello world"},
		{"latin accents kept", "Configuração à ré", "Configuração à ré"},
		{"bmp symbols kept", "€ — → ·", "€ — → ·"},
		{"astral emoji dropped", "status 🟢 ok", "status  ok"},
		{"mixed emoji dropped", "🟢🟡🔴 x", " x"},
		{"keeps surrounding text", "a🔴b", "ab"},
	}
	for _, tt := range tests {
		got := string(stripNonBMP([]byte(tt.in)))
		if got != tt.want {
			t.Errorf("%s: stripNonBMP(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
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
