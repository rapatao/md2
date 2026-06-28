// Package converter defines the conversion interface and a registry of
// available output formats. New formats register themselves here so the CLI
// can stay agnostic about which targets exist.
package converter

import (
	"fmt"
	"io"
	"sort"
)

// Converter turns markdown source into some output format, written to w.
type Converter interface {
	// Convert reads markdown bytes and writes the converted document to w.
	Convert(src []byte, w io.Writer) error
}

// registry maps a format key (e.g. "pdf") to its Converter.
var registry = map[string]Converter{}

// Register adds a converter under the given format key. It panics on a
// duplicate key, since that can only be a programming error.
func Register(format string, c Converter) {
	if _, exists := registry[format]; exists {
		panic(fmt.Sprintf("converter: format %q already registered", format))
	}
	registry[format] = c
}

// Get returns the converter for a format, or an error if none is registered.
func Get(format string) (Converter, error) {
	c, ok := registry[format]
	if !ok {
		return nil, fmt.Errorf("unsupported format %q (have: %v)", format, Formats())
	}
	return c, nil
}

// Formats lists the registered format keys, sorted.
func Formats() []string {
	out := make([]string, 0, len(registry))
	for f := range registry {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
