package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/rapatao/md2/internal/converter"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// flagSet builds the CLI flag set, binding the flags to the given pointers.
func flagSet(output, format, render, css, plantumlServer *string, allowDownload, flatten, stdout, showVersion *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("md2", flag.ContinueOnError)
	fs.StringVar(output, "o", "", "output file (default: first input's name with new extension)")
	fs.StringVar(format, "f", "", fmt.Sprintf("output format(s), comma-separated %v (default: from -o extension, else pdf)", converter.Formats()))
	fs.StringVar(render, "render", "", fmt.Sprintf("diagram renderer(s) to enable, comma-separated %v or \"all\" (default: none)", htmlconv.SupportedDiagrams()))
	fs.BoolVar(flatten, "flatten", false, "flatten HTML diagrams to static images instead of inlining mermaid.js (self-contained, e.g. for Google Docs; needs a browser)")
	fs.StringVar(css, "css", "", "path to a CSS file appended after the built-in stylesheet in HTML output; also forces the browser-rendered PDF path, since the pure-Go PDF renderer has no CSS support")
	fs.StringVar(plantumlServer, "plantuml-server", htmlconv.PlantUMLServer, "PlantUML server base URL used to render plantuml diagrams to SVG (the diagram source is sent to this server)")
	fs.BoolVar(allowDownload, "allow-download", false, "authorize downloading Chromium for the browser renderer without prompting")
	fs.BoolVar(stdout, "stdout", false, "write the converted result to standard output instead of a file (single format only; also writes a file when -o is given)")
	fs.BoolVar(showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: md2 [-o output] [-f format] [-render diagrams] [-flatten] [-css file] [-plantuml-server url] [-stdout] input.md [input2.md ...]")
		fs.PrintDefaults()
	}
	return fs
}
