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

	"github.com/rapatao/md2/internal/converter"
	"github.com/rapatao/md2/internal/merge"

	// Register the remaining output formats via their init funcs.
	_ "github.com/rapatao/md2/internal/converter/docx"
	_ "github.com/rapatao/md2/internal/converter/epub"
	_ "github.com/rapatao/md2/internal/converter/pdf"
	_ "github.com/rapatao/md2/internal/converter/text"
)

// Run parses args and performs the requested conversion(s). version is
// printed for -version and is resolved by the caller (main), which alone
// knows about build-time ldflags. stdin is read for any "-" input (typically
// os.Stdin; tests pass a buffer). stdoutWriter is where -stdout streams the
// converted result (typically os.Stdout; tests pass a buffer).
func Run(args []string, version string, stdin io.Reader, stdoutWriter io.Writer) error {
	var o options
	fs := newFlagSet(&o)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if o.showVersion {
		fmt.Println("md2", version)
		return nil
	}

	if err := o.apply(); err != nil {
		return err
	}

	inputs := fs.Args()
	if len(inputs) < 1 {
		fs.Usage()
		return fmt.Errorf("expected at least one input file, got %d", len(inputs))
	}
	inputs, err := expandDir(inputs, o.recursive)
	if err != nil {
		return err
	}

	formats := resolveFormats(o.format, o.output)
	if err := validate(&o, inputs, formats); err != nil {
		return err
	}

	// Resolve every converter up front so an unknown format fails fast,
	// before we write any output.
	convs, err := getConverters(formats)
	if err != nil {
		return err
	}

	// -per-file converts each input independently to its own output next to the
	// source, rather than merging.
	if o.perFile {
		return runPerFile(convs, formats, inputs, stdin)
	}

	// Merge every input into one document. srcPath resolves relative image
	// refs; stdin has no directory, so it falls back to cwd.
	src, err := merge.Inputs(inputs, stdin)
	if err != nil {
		return err
	}
	srcPath := inputs[0]
	if srcPath == "-" {
		srcPath = "."
	}

	dsts := outputPaths(o.output, inputs[0], formats)

	if o.stdout {
		return runStdout(convs[0], src, srcPath, o.output, dsts[0], stdoutWriter)
	}
	return writeEach(convs, srcPath, src, dsts)
}

// expandDir replaces a lone directory argument with its .md files (its whole
// tree with recursive), ordered folder by folder. Any other input list passes
// through unchanged.
func expandDir(inputs []string, recursive bool) ([]string, error) {
	if len(inputs) == 1 {
		if info, err := os.Stat(inputs[0]); err == nil && info.IsDir() {
			return markdownFiles(inputs[0], recursive)
		}
	}
	return inputs, nil
}

// resolveFormats picks the output formats: explicit -f (comma list) wins, else
// the -o extension, else the default pdf.
func resolveFormats(format, output string) []string {
	if formats := parseList(format); len(formats) > 0 {
		return formats
	}
	if ext := strings.TrimPrefix(filepath.Ext(output), "."); ext != "" {
		return []string{ext}
	}
	return []string{"pdf"}
}

// validate rejects flag/input/format combinations that cannot produce output.
func validate(o *options, inputs, formats []string) error {
	switch {
	// Reading from stdin ("-") has no basename to derive a default output name
	// from, so an explicit destination is required.
	case inputs[0] == "-" && o.output == "" && !o.stdout:
		return fmt.Errorf("reading markdown from stdin (-) requires -o or -stdout")

	// -per-file writes one output per input next to its source, so a single
	// destination (-o or -stdout) is meaningless with it.
	case o.perFile && (o.output != "" || o.stdout):
		return fmt.Errorf("-per-file cannot be combined with -o/-stdout")

	// Merging multiple inputs into one document has no obvious output name, so
	// require an explicit destination (or -per-file to split them instead).
	case !o.perFile && len(inputs) > 1 && o.output == "" && !o.stdout:
		return fmt.Errorf("merging %d inputs requires -o (or -per-file to convert each separately)", len(inputs))

	// An explicit -o names a single file, so it cannot serve many formats.
	case o.output != "" && len(formats) > 1:
		return fmt.Errorf("-o cannot be used with multiple formats %v; omit -o or pass one format", formats)

	// -stdout writes to a single stream, so it cannot serve many formats (their
	// bytes would interleave).
	case o.stdout && len(formats) > 1:
		return fmt.Errorf("-stdout cannot be used with multiple formats %v; pass one format", formats)
	}
	return nil
}

// getConverters resolves one converter per format, erroring on the first
// unknown format.
func getConverters(formats []string) ([]converter.Converter, error) {
	convs := make([]converter.Converter, len(formats))
	for i, format := range formats {
		conv, err := converter.Get(format)
		if err != nil {
			return nil, err
		}
		convs[i] = conv
	}
	return convs, nil
}

// outputPaths resolves a destination per format. An explicit -o names the file
// (validate has ensured it maps to a single format); otherwise the first
// input's basename gets the format as its extension.
func outputPaths(output, firstInput string, formats []string) []string {
	dsts := make([]string, len(formats))
	for i, format := range formats {
		if output != "" {
			dsts[i] = output
			continue
		}
		dsts[i] = strings.TrimSuffix(firstInput, filepath.Ext(firstInput)) + "." + format
	}
	return dsts
}

// runPerFile converts each input independently to its own output next to the
// source. One failing input/format must not stop the rest, so errors are
// collected and joined.
func runPerFile(convs []converter.Converter, formats, inputs []string, stdin io.Reader) error {
	var errs []error
	for _, in := range inputs {
		src, err := merge.Inputs([]string{in}, stdin)
		if err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "md2: %v\n", err)
			continue
		}
		dsts := make([]string, len(formats))
		for i, format := range formats {
			dsts[i] = strings.TrimSuffix(in, filepath.Ext(in)) + "." + format
		}
		if err := writeEach(convs, in, src, dsts); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// runStdout streams the single converted result to stdoutWriter. With -o it
// also writes the file, sending the "wrote" notice to stderr so the converted
// bytes on stdout stay uncorrupted.
func runStdout(conv converter.Converter, src []byte, srcPath, output, dst string, stdoutWriter io.Writer) error {
	var buf bytes.Buffer
	if err := convert(conv, src, srcPath, &buf); err != nil {
		return fmt.Errorf("convert: %w", err)
	}
	if output != "" {
		if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", dst)
	}
	if _, err := stdoutWriter.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// writeEach converts src once per (converter, dst) pair, printing a notice per
// file. One failing format must not stop the others, so errors are collected
// and joined.
func writeEach(convs []converter.Converter, srcPath string, src []byte, dsts []string) error {
	var errs []error
	for i, conv := range convs {
		if err := writeOutput(conv, src, srcPath, dsts[i]); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "md2: %v\n", err)
			continue
		}
		fmt.Printf("wrote %s\n", dsts[i])
	}
	return errors.Join(errs...)
}

// markdownFiles lists the .md files under dir (top-level, or the whole tree
// with recursive), ordered folder by folder, and errors if there are none.
func markdownFiles(dir string, recursive bool) ([]string, error) {
	files, err := collectMarkdown(dir, recursive)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .md files in %s", dir)
	}
	return files, nil
}

// collectMarkdown returns dir's own .md files (sorted by name), then each
// sub-directory's, recursively — so a folder's files stay contiguous and come
// before its sub-folders. os.ReadDir returns entries already sorted by name.
// It does not error on an empty sub-directory; the caller checks the total.
func collectMarkdown(dir string, recursive bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files, subdirs []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if recursive {
				subdirs = append(subdirs, p)
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			files = append(files, p)
		}
	}
	for _, sub := range subdirs {
		sf, err := collectMarkdown(sub, recursive)
		if err != nil {
			return nil, err
		}
		files = append(files, sf...)
	}
	return files, nil
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
