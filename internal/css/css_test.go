package css

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCSS(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPlain(t *testing.T) {
	dir := t.TempDir()
	path := writeCSS(t, dir, "extra.css", "body{background:#eef}")
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out != "body{background:#eef}" {
		t.Errorf("Load = %q, want unchanged content", out)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nonexistent.css")); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadLocalImport(t *testing.T) {
	dir := t.TempDir()
	writeCSS(t, dir, "base.css", "body{color:blue}")
	path := writeCSS(t, dir, "extra.css", `@import "base.css"; h1{color:red}`)

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(out, "body{color:blue}") {
		t.Errorf("expected imported CSS inlined:\n%s", out)
	}
	if !strings.Contains(out, "h1{color:red}") {
		t.Errorf("expected importing CSS preserved:\n%s", out)
	}
	if strings.Contains(out, "@import") {
		t.Errorf("local @import should be inlined, not left in output:\n%s", out)
	}
}

func TestLoadNestedImport(t *testing.T) {
	dir := t.TempDir()
	writeCSS(t, dir, "grandchild.css", "a{color:green}")
	writeCSS(t, dir, "child.css", `@import url(grandchild.css);`)
	path := writeCSS(t, dir, "extra.css", `@import "child.css";`)

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(out, "a{color:green}") {
		t.Errorf("expected nested import resolved:\n%s", out)
	}
}

func TestLoadImportCycle(t *testing.T) {
	dir := t.TempDir()
	writeCSS(t, dir, "a.css", `@import "b.css"; a{color:red}`)
	writeCSS(t, dir, "b.css", `@import "a.css"; b{color:blue}`)

	// A cycle must not hang or crash Load; the repeat import is dropped.
	out, err := Load(filepath.Join(dir, "a.css"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(out, "a{color:red}") || !strings.Contains(out, "b{color:blue}") {
		t.Errorf("expected both cyclic files' rules present:\n%s", out)
	}
}

func TestLoadRemoteImportUntouched(t *testing.T) {
	dir := t.TempDir()
	path := writeCSS(t, dir, "extra.css", `@import url(https://example.com/base.css); h1{color:red}`)

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(out, `@import url(https://example.com/base.css);`) {
		t.Errorf("expected remote @import left untouched for the browser to fetch:\n%s", out)
	}
}

// A symlink-routed cycle must be caught the same as a direct one: the cycle
// guard resolves to a canonical, symlink-free path before checking visited,
// so this must not hang or crash (infinite recursion) if the guard regresses.
func TestLoadSymlinkImportCycle(t *testing.T) {
	dir := t.TempDir()
	a := writeCSS(t, dir, "a.css", `@import "link.css"; a{color:red}`)
	link := filepath.Join(dir, "link.css")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	out, err := Load(a)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(out, "a{color:red}") {
		t.Errorf("expected content preserved through symlink cycle:\n%s", out)
	}
}

func TestLoadCommentedImportIgnored(t *testing.T) {
	dir := t.TempDir()
	writeCSS(t, dir, "old-theme.css", "body{color:purple}")
	path := writeCSS(t, dir, "extra.css", `/* @import "old-theme.css"; */ h1{color:red}`)

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(out, "purple") {
		t.Errorf("commented-out @import must not be inlined:\n%s", out)
	}
	if strings.Contains(out, "@import") {
		t.Errorf("expected comment (and the @import inside it) stripped:\n%s", out)
	}
	if !strings.Contains(out, "h1{color:red}") {
		t.Errorf("expected live CSS after the comment preserved:\n%s", out)
	}
}

func TestLoadMissingNestedImportDropped(t *testing.T) {
	dir := t.TempDir()
	path := writeCSS(t, dir, "extra.css", `@import "missing.css"; h1{color:red}`)

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(out, "@import") || strings.Contains(out, "missing.css") {
		t.Errorf("a failed nested import must be dropped, not left as a dangling relative reference:\n%s", out)
	}
	if !strings.Contains(out, "h1{color:red}") {
		t.Errorf("expected live CSS after the failed import preserved:\n%s", out)
	}
}
