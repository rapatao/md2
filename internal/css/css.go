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
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	data = resolveImports(data, filepath.Dir(path), map[string]bool{abs: true})
	return string(data), nil
}

// importRe matches an @import statement naming a local or remote stylesheet,
// in any of its "url(...)"/quoted-string forms, up to the terminating ";"
// (which may be preceded by a media query).
var importRe = regexp.MustCompile(`@import\s+(?:url\(\s*)?['"]?([^'")\s;]+)['"]?\)?[^;]*;`)

// resolveImports inlines local @import targets referenced from src,
// resolving relative paths against baseDir, recursively. visited (keyed by
// absolute path, seeded with the top-level stylesheet) guards against import
// cycles and duplicate inlining of a diamond-imported file; either causes the
// repeat import to be dropped rather than looped forever.
func resolveImports(src []byte, baseDir string, visited map[string]bool) []byte {
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
		abs, err := filepath.Abs(path)
		if err != nil || visited[abs] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "md2: cannot inline CSS import %q: %v\n", target, err)
			return m
		}
		visited[abs] = true
		return resolveImports(data, filepath.Dir(path), visited)
	})
}
