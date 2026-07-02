// Package merge concatenates multiple markdown files, in order, into a
// single source document.
package merge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// Inputs reads and concatenates the given files, in order, into a single
// markdown source separated by blank lines (forcing a fresh block boundary so
// the last line of one file never merges into the first line of the next).
// With more than one input, each file's relative image references are
// rewritten to absolute paths against its own directory first, since the
// merged document has no single directory to resolve them against.
func Inputs(inputs []string) ([]byte, error) {
	parts := make([][]byte, len(inputs))
	for i, in := range inputs {
		src, err := os.ReadFile(in)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", in, err)
		}
		if len(inputs) > 1 {
			src = rewriteRelativeImagePaths(src, filepath.Dir(in))
		}
		parts[i] = bytes.TrimRight(src, "\n")
	}
	return bytes.Join(parts, []byte("\n\n")), nil
}

// mdImageRe matches a markdown image reference, capturing its opening
// `![alt](`, its destination, and everything up to and including the closing
// paren (an optional " title" plus ")").
var mdImageRe = regexp.MustCompile(`(!\[[^\]]*\]\()([^)\s]+)([^)]*\))`)

// rewriteRelativeImagePaths rewrites relative markdown image destinations to
// absolute paths against baseDir, so they still resolve once concatenated
// with files from other directories. URLs and already-absolute paths are
// left untouched.
func rewriteRelativeImagePaths(src []byte, baseDir string) []byte {
	return mdImageRe.ReplaceAllFunc(src, func(m []byte) []byte {
		g := mdImageRe.FindSubmatch(m)
		dest := string(g[2])
		if dest == "" || filepath.IsAbs(dest) || htmlconv.HasURLScheme(dest) {
			return m
		}
		abs := filepath.Join(baseDir, filepath.FromSlash(dest))
		return append(append(append([]byte(nil), g[1]...), abs...), g[3]...)
	})
}
