package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rapatao/md2/internal/converter"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// flagSet builds the CLI flag set, binding the flags to the given pointers.
func flagSet(output, format, render *string, allowDownload, flatten, showVersion *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("md2", flag.ContinueOnError)
	fs.StringVar(output, "o", "", "output file (default: input name with new extension)")
	fs.StringVar(format, "f", "", fmt.Sprintf("output format(s), comma-separated %v (default: from -o extension, else pdf)", converter.Formats()))
	fs.StringVar(render, "render", "", fmt.Sprintf("diagram renderer(s) to enable, comma-separated %v or \"all\" (default: none)", htmlconv.SupportedDiagrams()))
	fs.BoolVar(flatten, "flatten", false, "flatten HTML diagrams to static images instead of inlining mermaid.js (self-contained, e.g. for Google Docs; needs a browser)")
	fs.BoolVar(allowDownload, "allow-download", false, "authorize downloading Chromium for the browser renderer without prompting")
	fs.BoolVar(showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: md2 [-o output] [-f format] [-render diagrams] [-flatten] input.md")
		fs.PrintDefaults()
	}
	return fs
}
