// Package urlref classifies markdown/HTML reference strings (image and link
// destinations) as local paths or URLs.
package urlref

import "strings"

// HasScheme reports whether s starts with a URL scheme (e.g. "https:") or is
// protocol-relative ("//host/..."), i.e. a non-local reference.
func HasScheme(s string) bool {
	if strings.HasPrefix(s, "//") {
		return true
	}
	if i := strings.IndexByte(s, ':'); i > 0 {
		for _, r := range s[:i] {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
				return false
			}
		}
		return true
	}
	return false
}
