// Command md2 converts markdown files to other formats (currently PDF).
//
// Usage:
//
//	md2 [-o output] [-f format] input.md [input2.md ...]
//
// With more than one input file, they are concatenated in the order given
// into a single document before conversion. Each file's relative image
// references are resolved against its own directory.
//
// An input of "-" reads markdown from stdin (flags must precede it); this
// requires -o or -stdout, since there is no input name to derive one from.
//
// If -o is omitted, the output filename is the (first) input with its
// extension replaced by the format. If -f is omitted, the format is inferred
// from the output extension, defaulting to pdf.
//
// With -stdout the converted result is written to standard output instead of a
// file (single format only); pass -o as well to also write the file.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/rapatao/md2/internal/cli"
)

// version is the build version, overridden at release time via -ldflags.
var version = "dev"

// resolveVersion returns version, falling back to the module version embedded
// by `go install pkg@version` when ldflags didn't set one (e.g. dev builds).
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	if err := cli.Run(os.Args[1:], resolveVersion(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "md2:", err)
		os.Exit(1)
	}
}
