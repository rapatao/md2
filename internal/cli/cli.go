// Package cli implements md2's command-line orchestration: flag parsing,
// input merging, and dispatching to the registered converters.
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rapatao/md2/internal/consent"
	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/chrome"
	"github.com/rapatao/md2/internal/css"
	"github.com/rapatao/md2/internal/merge"

	htmlconv "github.com/rapatao/md2/internal/converter/html"

	// Register the remaining output formats via their init funcs.
	_ "github.com/rapatao/md2/internal/converter/pdf"
	_ "github.com/rapatao/md2/internal/converter/text"
)

// Run parses args and performs the requested conversion(s). version is
// printed for -version and is resolved by the caller (main), which alone
// knows about build-time ldflags. stdoutWriter is where -stdout streams the
// converted result (typically os.Stdout; tests pass a buffer).
func Run(args []string, version string, stdoutWriter io.Writer) error {
	var (
		output            string
		format            string
		render            string
		cssPath           string
		plantumlServer    string
		allowDownload     bool
		flatten           bool
		keepDiagramSource bool
		stdout            bool
		showVersion       bool
	)

	fs := flagSet(&output, &format, &render, &cssPath, &plantumlServer, &allowDownload, &flatten, &keepDiagramSource, &stdout, &showVersion)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if showVersion {
		fmt.Println("md2", version)
		return nil
	}

	// Enable any diagram renderers requested via -render. Off by default.
	if err := htmlconv.EnableDiagrams(parseList(render)); err != nil {
		return err
	}

	// -plantuml-server points the plantuml renderer at a PlantUML server; an
	// empty value keeps the built-in default (the public plantuml.com server).
	if plantumlServer != "" {
		htmlconv.PlantUMLServer = plantumlServer
	}

	// -flatten renders HTML diagrams to static images rather than inlining
	// mermaid.js, for a self-contained file (e.g. importable into Google Docs).
	htmlconv.Flatten = flatten

	// -keep-diagram-source keeps the original diagram source in the output in
	// addition to the rendered diagram (rendered first, then the source block).
	htmlconv.KeepDiagramSource = keepDiagramSource

	// -css appends a user stylesheet after the built-in defaults in HTML
	// output (and the browser-rendered PDF fallback); the pure-Go PDF path
	// ignores it.
	htmlconv.ExtraCSS = ""
	if cssPath != "" {
		extraCSS, err := css.Load(cssPath)
		if err != nil {
			return fmt.Errorf("reading -css file: %w", err)
		}
		htmlconv.ExtraCSS = extraCSS
	}

	// Decide how the PDF browser fallback may obtain a browser if none exists.
	chrome.Consent = consent.Policy(allowDownload)

	inputs := fs.Args()
	if len(inputs) < 1 {
		fs.Usage()
		return fmt.Errorf("expected at least one input file, got %d", len(inputs))
	}

	// Resolve formats: explicit -f (comma list) > output extension > default pdf.
	formats := parseList(format)
	if len(formats) == 0 {
		if ext := strings.TrimPrefix(filepath.Ext(output), "."); ext != "" {
			formats = []string{ext}
		} else {
			formats = []string{"pdf"}
		}
	}

	// An explicit -o names a single file, so it cannot serve many formats.
	if output != "" && len(formats) > 1 {
		return fmt.Errorf("-o cannot be used with multiple formats %v; omit -o or pass one format", formats)
	}

	// -stdout writes to a single stream, so it cannot serve many formats (their
	// bytes would interleave).
	if stdout && len(formats) > 1 {
		return fmt.Errorf("-stdout cannot be used with multiple formats %v; pass one format", formats)
	}

	// Resolve every converter up front so an unknown format fails fast,
	// before we write any output.
	convs := make([]converter.Converter, len(formats))
	for i, format := range formats {
		conv, err := converter.Get(format)
		if err != nil {
			return err
		}
		convs[i] = conv
	}

	// Resolve every output path up front. The format key doubles as the file
	// extension, and -f deduplicates, so distinct formats never collide.
	// With multiple inputs, the first one names the merged output.
	dsts := make([]string, len(formats))
	for i, format := range formats {
		dst := output
		if dst == "" {
			base := strings.TrimSuffix(inputs[0], filepath.Ext(inputs[0]))
			dst = base + "." + format
		}
		dsts[i] = dst
	}

	src, err := merge.Inputs(inputs)
	if err != nil {
		return err
	}
	srcPath := inputs[0]

	// -stdout streams the (single) converted result to standard output. With -o
	// it also writes the file; the "wrote" notice goes to stderr to keep the
	// converted bytes on stdout uncorrupted.
	if stdout {
		var buf bytes.Buffer
		if err := convert(convs[0], src, srcPath, &buf); err != nil {
			return fmt.Errorf("convert: %w", err)
		}
		if output != "" {
			if err := os.WriteFile(dsts[0], buf.Bytes(), 0o644); err != nil {
				return fmt.Errorf("create output: %w", err)
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", dsts[0])
		}
		if _, err := stdoutWriter.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}

	// Convert each format independently: one failing format must not stop the
	// others (e.g. a PDF render error should still let HTML be written).
	var errs []error
	for i := range formats {
		dst := dsts[i]

		if err := writeOutput(convs[i], src, srcPath, dst); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "md2: %v\n", err)
			continue
		}
		fmt.Printf("wrote %s\n", dst)
	}
	return errors.Join(errs...)
}

// parseList splits a comma-separated list, trimming blanks and dropping
// duplicates while preserving order.
func parseList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// writeOutput converts src and writes the result to a new file at path.
func writeOutput(conv converter.Converter, src []byte, srcPath, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	if err := convert(conv, src, srcPath, out); err != nil {
		return fmt.Errorf("convert %s: %w", path, err)
	}
	return nil
}

// convert runs the converter, writing the result to w. A converter that
// implements converter.PathConverter is given the input path too, so it can
// resolve relative references (e.g. local images).
func convert(conv converter.Converter, src []byte, srcPath string, w io.Writer) error {
	if pc, ok := conv.(converter.PathConverter); ok {
		return pc.ConvertFrom(src, srcPath, w)
	}
	return conv.Convert(src, w)
}
