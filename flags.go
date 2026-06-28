package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rapatao/md2/internal/converter"
)

// flagSet builds the CLI flag set, binding the flags to the given pointers.
func flagSet(output, format *string, allowDownload *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("md2", flag.ContinueOnError)
	fs.StringVar(output, "o", "", "output file (default: input name with new extension)")
	fs.StringVar(format, "f", "", fmt.Sprintf("output format(s), comma-separated %v (default: from -o extension, else pdf)", converter.Formats()))
	fs.BoolVar(allowDownload, "allow-download", false, "authorize downloading Chromium for the PDF browser fallback without prompting")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: md2 [-o output] [-f format] input.md")
		fs.PrintDefaults()
	}
	return fs
}
