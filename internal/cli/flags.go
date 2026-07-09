package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/rapatao/md2/internal/converter"
	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// flagSet builds the CLI flag set, binding the flags to the given pointers.
func flagSet(output, format, render, css, plantumlServer *string, allowDownload, flatten, keepDiagramSource, stdout, perFile, recursive, showVersion *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("md2", flag.ContinueOnError)
	fs.StringVar(output, "o", "", "output file (default: input's name with new extension; required when merging multiple inputs)")
	fs.StringVar(format, "f", "", fmt.Sprintf("output format(s), comma-separated %v (default: from -o extension, else pdf)", converter.Formats()))
	fs.StringVar(render, "render", "", fmt.Sprintf("diagram renderer(s) to enable, comma-separated %v or \"all\" (default: none)", htmlconv.SupportedDiagrams()))
	fs.BoolVar(flatten, "flatten", false, "flatten HTML diagrams to static images instead of inlining mermaid.js (self-contained, e.g. for Google Docs; needs a browser)")
	fs.BoolVar(keepDiagramSource, "keep-diagram-source", false, "keep the original diagram source in the output in addition to the rendered diagram (rendered first, then the source block)")
	fs.StringVar(css, "css", "", "path to a CSS file appended after the built-in stylesheet in HTML output; also forces the browser-rendered PDF path, since the pure-Go PDF renderer has no CSS support")
	fs.StringVar(plantumlServer, "plantuml-server", htmlconv.PlantUMLServer, "PlantUML server base URL used to render plantuml diagrams to SVG (the diagram source is sent to this server)")
	fs.BoolVar(allowDownload, "allow-download", false, "authorize downloading Chromium for the browser renderer without prompting")
	fs.BoolVar(stdout, "stdout", false, "write the converted result to standard output instead of a file (single format only; also writes a file when -o is given)")
	fs.BoolVar(perFile, "per-file", false, "with multiple inputs (or a directory), convert each to its own output next to its source instead of merging (cannot be combined with -o/-stdout)")
	fs.BoolVar(recursive, "recursive", false, "when the input is a directory, also pick up .md files in sub-directories (folder by folder)")
	fs.BoolVar(showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: md2 [-o output] [-f format] [-render diagrams] [-flatten] [-keep-diagram-source] [-css file] [-plantuml-server url] [-stdout] [-per-file] [-recursive] input.md [input2.md ...] | dir/")
		fs.PrintDefaults()
	}
	return fs
}
