// Package css loads a user-supplied stylesheet, inlining any local @import
// targets it references.
package css

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/rapatao/md2/internal/urlref"
)

// Load reads the stylesheet at path and inlines its local @import targets,
// recursively, so the result is self-contained. Remote imports
// (scheme://) are left untouched for the browser to fetch itself.
func Load(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	abs, err := canonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	data = resolveImports(data, filepath.Dir(path), map[string]bool{abs: true})
	return string(data), nil
}

// commentRe matches a CSS block comment. CSS has no other comment syntax and
// no nested comments, so a non-greedy match is exact. Comments are stripped
// before import resolution so a commented-out @import (e.g. an old theme,
// left disabled while swapping) is never mistaken for a live one.
var commentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// importRe matches an @import statement naming a local or remote stylesheet,
// in any of its "url(...)"/quoted-string forms, up to the terminating ";"
// (which may be preceded by a media query).
var importRe = regexp.MustCompile(`@import\s+(?:url\(\s*)?['"]?([^'")\s;]+)['"]?\)?[^;]*;`)

// resolveImports inlines local @import targets referenced from src,
// resolving relative paths against baseDir, recursively. visited (keyed by
// canonical, symlink-resolved absolute path, seeded with the top-level
// stylesheet) guards against import cycles — including ones routed through a
// symlink — and duplicate inlining of a diamond-imported file; either causes
// the repeat import to be dropped rather than looped forever.
func resolveImports(src []byte, baseDir string, visited map[string]bool) []byte {
	src = commentRe.ReplaceAll(src, nil)
	return importRe.ReplaceAllFunc(src, func(m []byte) []byte {
		g := importRe.FindSubmatch(m)
		target := string(g[1])
		if urlref.HasScheme(target) {
			return m
		}

		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, filepath.FromSlash(target))
		}
		abs, err := canonicalPath(path)
		if err != nil || visited[abs] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			// A relative reference left as-is here would be resolved by the
			// browser against the final document's location, not this CSS
			// file's directory — almost certainly wrong. Drop it instead of
			// baking in a broken or misleading path.
			fmt.Fprintf(os.Stderr, "md2: cannot inline CSS import %q: %v\n", target, err)
			return nil
		}
		visited[abs] = true
		return resolveImports(data, filepath.Dir(path), visited)
	})
}

// canonicalPath resolves path to an absolute, symlink-resolved form, used as
// the cycle-detection key so two different routes to the same file (e.g. one
// through a symlink) are recognized as identical. If the path doesn't exist
// (e.g. broken symlink, or not yet read), symlink resolution is skipped and
// the plain absolute path is used — the subsequent read will fail on its own.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}
