package merge

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInputsSingleUnchanged(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	writeFile(t, a, "# A\n\n![fig](fig.png)\n")

	got, err := Inputs([]string{a})
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	// A single input is passed through untouched (image paths not rewritten).
	want := "# A\n\n![fig](fig.png)"
	if string(got) != want {
		t.Errorf("Inputs(1 file) = %q, want %q", got, want)
	}
}

func TestInputsJoinsWithBlankLine(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	writeFile(t, a, "first")
	writeFile(t, b, "second")

	got, err := Inputs([]string{a, b})
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	want := "first\n\nsecond"
	if string(got) != want {
		t.Errorf("Inputs(2 files) = %q, want %q", got, want)
	}
}

func TestInputsRewritesImagesPerOwnDir(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "doc.md")
	b := filepath.Join(dir, "b", "doc.md")
	writeFile(t, a, "![x](fig.png)")
	writeFile(t, b, "![y](../shared/fig.png)")

	got, err := Inputs([]string{a, b})
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}

	wantA := filepath.Join(dir, "a", "fig.png")
	wantB := filepath.Join(dir, "shared", "fig.png") // b/../shared
	if !bytes.Contains(got, []byte("]("+wantA+")")) {
		t.Errorf("expected %q resolved against a/'s own dir, got %q", wantA, got)
	}
	if !bytes.Contains(got, []byte("]("+wantB+")")) {
		t.Errorf("expected %q resolved against b/'s own dir, got %q", wantB, got)
	}
}

func TestInputsLeavesAbsoluteAndURLImagesAlone(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	writeFile(t, a, "![abs](/already/absolute.png)")
	writeFile(t, b, "![url](https://example.com/fig.png)")

	got, err := Inputs([]string{a, b})
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	if !bytes.Contains(got, []byte("](/already/absolute.png)")) {
		t.Errorf("absolute path was rewritten: %q", got)
	}
	if !bytes.Contains(got, []byte("](https://example.com/fig.png)")) {
		t.Errorf("URL was rewritten: %q", got)
	}
}

func TestInputsMissingFile(t *testing.T) {
	if _, err := Inputs([]string{filepath.Join(t.TempDir(), "nope.md")}); err == nil {
		t.Fatal("expected error for missing input file")
	}
}
