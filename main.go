// Command md2 converts markdown files to other formats (currently PDF).
//
// Usage:
//
//	md2 [-o output] [-f format] input.md
//
// If -o is omitted, the output filename is the input with its extension
// replaced by the format. If -f is omitted, the format is inferred from the
// output extension, defaulting to pdf.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/converter/chrome"

	htmlconv "github.com/rapatao/md2/internal/converter/html"

	// Register the remaining output formats via their init funcs.
	_ "github.com/rapatao/md2/internal/converter/pdf"
	_ "github.com/rapatao/md2/internal/converter/text"
)

// version is the build version, overridden at release time via -ldflags.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "md2:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var (
		output        string
		format        string
		render        string
		allowDownload bool
		flatten       bool
		showVersion   bool
	)

	fs := flagSet(&output, &format, &render, &allowDownload, &flatten, &showVersion)
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

	// -flatten renders HTML diagrams to static images rather than inlining
	// mermaid.js, for a self-contained file (e.g. importable into Google Docs).
	htmlconv.Flatten = flatten

	// Decide how the PDF browser fallback may obtain a browser if none exists.
	chrome.Consent = consentFunc(allowDownload)

	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one input file, got %d", len(rest))
	}
	input := rest[0]

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
	dsts := make([]string, len(formats))
	for i, format := range formats {
		dst := output
		if dst == "" {
			base := strings.TrimSuffix(input, filepath.Ext(input))
			dst = base + "." + format
		}
		dsts[i] = dst
	}

	src, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	// Convert each format independently: one failing format must not stop the
	// others (e.g. a PDF render error should still let HTML be written).
	var errs []error
	for i := range formats {
		dst := dsts[i]

		if err := writeOutput(convs[i], src, input, dst); err != nil {
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

// consentFunc builds the policy the PDF browser fallback uses to decide
// whether to download a browser when none is installed. With -allow-download
// it always consents; otherwise it prompts on an interactive terminal and
// denies when there is no terminal to prompt on.
func consentFunc(allowDownload bool) func() (bool, error) {
	return func() (bool, error) {
		if allowDownload {
			return true, nil
		}
		if !stdinIsTerminal() {
			return false, nil
		}
		fmt.Fprint(os.Stderr, "No Chrome/Chromium found. Download Chromium (~150MB) to render the PDF? [y/N]: ")
		return readConsent(os.Stdin)
	}
}

// readConsent reads a single line and reports whether it affirms (y/yes,
// case-insensitive). Anything else, or a read error, is treated as a decline.
func readConsent(r io.Reader) (bool, error) {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// stdinIsTerminal reports whether standard input is an interactive terminal.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// writeOutput runs the converter and writes the result to path. A converter
// that implements converter.PathConverter is given the input path too, so it
// can resolve relative references (e.g. local images).
func writeOutput(conv converter.Converter, src []byte, srcPath, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	if pc, ok := conv.(converter.PathConverter); ok {
		err = pc.ConvertFrom(src, srcPath, out)
	} else {
		err = conv.Convert(src, out)
	}
	if err != nil {
		return fmt.Errorf("convert %s: %w", path, err)
	}
	return nil
}
