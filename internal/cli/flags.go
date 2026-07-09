package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/rapatao/md2/internal/consent"
	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/chrome"
	"github.com/rapatao/md2/internal/css"

	htmlconv "github.com/rapatao/md2/internal/converter/html"
)

// options holds every value parsed from the command line.
type options struct {
	output            string
	format            string
	render            string
	cssPath           string
	plantumlServer    string
	allowDownload     bool
	flatten           bool
	keepDiagramSource bool
	stdout            bool
	perFile           bool
	recursive         bool
	showVersion       bool
}

// newFlagSet builds the CLI flag set with its flags bound to o.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("md2", flag.ContinueOnError)
	fs.StringVar(&o.output, "o", "", "output file (default: input's name with new extension; required when merging multiple inputs)")
	fs.StringVar(&o.format, "f", "", fmt.Sprintf("output format(s), comma-separated %v (default: from -o extension, else pdf)", converter.Formats()))
	fs.StringVar(&o.render, "render", "", fmt.Sprintf("diagram renderer(s) to enable, comma-separated %v or \"all\" (default: none)", htmlconv.SupportedDiagrams()))
	fs.BoolVar(&o.flatten, "flatten", false, "flatten HTML diagrams to static images instead of inlining mermaid.js (self-contained, e.g. for Google Docs; needs a browser)")
	fs.BoolVar(&o.keepDiagramSource, "keep-diagram-source", false, "keep the original diagram source in the output in addition to the rendered diagram (rendered first, then the source block)")
	fs.StringVar(&o.cssPath, "css", "", "path to a CSS file appended after the built-in stylesheet in HTML output; also forces the browser-rendered PDF path, since the pure-Go PDF renderer has no CSS support")
	fs.StringVar(&o.plantumlServer, "plantuml-server", htmlconv.PlantUMLServer, "PlantUML server base URL used to render plantuml diagrams to SVG (the diagram source is sent to this server)")
	fs.BoolVar(&o.allowDownload, "allow-download", false, "authorize downloading Chromium for the browser renderer without prompting")
	fs.BoolVar(&o.stdout, "stdout", false, "write the converted result to standard output instead of a file (single format only; also writes a file when -o is given)")
	fs.BoolVar(&o.perFile, "per-file", false, "with multiple inputs (or a directory), convert each to its own output next to its source instead of merging (cannot be combined with -o/-stdout)")
	fs.BoolVar(&o.recursive, "recursive", false, "when the input is a directory, also pick up .md files in sub-directories (folder by folder)")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: md2 [-o output] [-f format] [-render diagrams] [-flatten] [-keep-diagram-source] [-css file] [-plantuml-server url] [-stdout] [-per-file] [-recursive] input.md [input2.md ...] | dir/")
		fs.PrintDefaults()
	}
	return fs
}

// apply configures the shared renderer and browser globals from o, loading the
// -css file (the one step that can fail). It runs once, before conversion.
func (o *options) apply() error {
	// Enable any diagram renderers requested via -render. Off by default.
	if err := htmlconv.EnableDiagrams(parseList(o.render)); err != nil {
		return err
	}

	// -plantuml-server points the plantuml renderer at a PlantUML server; an
	// empty value keeps the built-in default (the public plantuml.com server).
	if o.plantumlServer != "" {
		htmlconv.PlantUMLServer = o.plantumlServer
	}

	// -flatten renders HTML diagrams to static images rather than inlining
	// mermaid.js, for a self-contained file (e.g. importable into Google Docs).
	htmlconv.Flatten = o.flatten

	// -keep-diagram-source keeps the original diagram source in the output in
	// addition to the rendered diagram (rendered first, then the source block).
	htmlconv.KeepDiagramSource = o.keepDiagramSource

	// -css appends a user stylesheet after the built-in defaults in HTML output
	// (and the browser-rendered PDF fallback); the pure-Go PDF path ignores it.
	htmlconv.ExtraCSS = ""
	if o.cssPath != "" {
		extraCSS, err := css.Load(o.cssPath)
		if err != nil {
			return fmt.Errorf("reading -css file: %w", err)
		}
		htmlconv.ExtraCSS = extraCSS
	}

	// Decide how the PDF browser fallback may obtain a browser if none exists.
	chrome.Consent = consent.Policy(o.allowDownload)
	return nil
}
