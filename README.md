# md2

[![CI](https://github.com/rapatao/md2/actions/workflows/ci.yml/badge.svg)](https://github.com/rapatao/md2/actions/workflows/ci.yml)

Convert markdown files to other formats. Pure Go by default, extensible to new output formats.

Currently supported:

- **PDF** (`.pdf`)
- **HTML** (`.html`) — self-contained; local images embedded as data URIs. Diagrams render via inlined mermaid.js, or as static images with `-flatten` (e.g. for Google Docs import)
- **Plain text** (`.txt`)

## Install

**Homebrew** (macOS):

```sh
brew install rapatao/tap/md2
```

**Nix** (flakes):

```sh
nix run github:rapatao/md2 -- input.md     # run without installing
nix profile install github:rapatao/md2     # install into your profile
```

**Prebuilt binaries**: download the archive for your OS/arch from the
[latest release](https://github.com/rapatao/md2/releases/latest).

**Go**:

```sh
go install github.com/rapatao/md2@latest
```

Or build locally:

```sh
go build -o md2 .
```

## Usage

```sh
md2 input.md                  # writes input.pdf (default format)
md2 -f html input.md          # writes input.html
md2 -f txt input.md           # writes input.txt (plain text)
md2 -f pdf,html input.md      # writes input.pdf and input.html
md2 -f html -render mermaid -flatten input.md  # self-contained html, diagrams as images (Google Docs)
md2 -o report.pdf input.md    # explicit output (format from extension)
md2 -f html -stdout input.md  # write html to stdout (no file), e.g. to pipe
```

Flags:

- `-o` output file. Default: input name with the format extension. Cannot be combined with multiple formats.
- `-f` output format(s), comma-separated. Default: inferred from `-o` extension, else `pdf`. Duplicates are ignored.
- `-render` diagram renderer(s) to enable, comma-separated (currently `mermaid`), or `all`. Default: none — diagrams render as plain code unless enabled.
- `-flatten` (HTML only) flatten diagrams to static images instead of inlining mermaid.js, for a self-contained file with no JS runtime needed to view it (e.g. importing into Google Docs). Requires a browser.
- `-stdout` write the converted result to standard output instead of a file, for piping into other tools. Single format only. With `-o` it also writes the file.
- `-allow-download` authorize downloading Chromium for the browser renderer without prompting (useful in CI).
- `-version` print the version and exit.

## PDF engine

PDF uses a two-stage strategy:

1. **Pure Go** (`goldmark-pdf`) — fast, no external runtime. Handles most documents.
2. **Headless browser fallback** — if the pure-Go renderer fails (e.g. complex
   tables, or glyphs like emoji it cannot lay out), md2 prints a styled HTML
   version to PDF with Chrome/Chromium for full fidelity.

The fallback prefers a browser already installed on the system. If none is
found it asks before downloading Chromium (~150MB, cached for later runs):

```
No Chrome/Chromium found. Download Chromium (~150MB) to render the PDF? [y/N]:
```

On a non-interactive terminal it declines unless `-allow-download` is passed.
Simple documents never launch a browser.

## Diagrams

Diagram rendering is **off by default** and enabled per run with `-render`.
Without it, a diagram code block is rendered as plain code.

````markdown
```mermaid
graph TD; A-->B;
```
````

```sh
md2 -f html -render mermaid input.md            # enable mermaid (interactive)
md2 -f html -render mermaid -flatten input.md   # diagrams as static images
md2 -f pdf  -render all     input.md            # enable every supported renderer
```

When enabled:

- **HTML** — by default the [mermaid](https://mermaid.js.org) library is inlined
  into the output (no network access needed to view it), and the block renders
  to SVG in the browser — interactive, but needing a JS runtime to display. With
  `-flatten`, md2 renders the document in a headless browser and replaces each
  diagram with a static PNG image, producing a self-contained file that displays
  anywhere — including a Google Docs import (upload the `.html` to Drive, then
  "Open with > Google Docs"), which runs no JavaScript.
- **PDF** — a mermaid block forces the headless-browser engine (the pure-Go
  renderer cannot run JavaScript), so the diagram is captured as vector graphics.
- **Plain text** — no diagram; the mermaid source is kept as code.

Inlining the library adds ~3 MB to each HTML file that contains a diagram;
`-flatten` avoids that (the diagrams become images instead). Files without a
diagram are unaffected either way.

Currently only `mermaid` is supported; the `-render` flag is designed to take
additional renderers (e.g. `plantuml`) in the future.

### Output naming

When `-o` is omitted, each output keeps the input's path and base name, swapping the
extension for the format — `docs/report.md` with `-f pdf,html` produces
`docs/report.pdf` and `docs/report.html`.

## Supported formats

| Format | Extension | Engine |
|--------|-----------|--------|
| `pdf`  | `.pdf`    | goldmark-pdf (pure Go), browser fallback (go-rod) |
| `html` | `.html`   | goldmark (GFM), styled standalone document; local images embedded as data URIs; diagrams as mermaid.js or, with `-flatten`, static PNGs (go-rod) |
| `txt`  | `.txt`    | goldmark AST walker, markup stripped, structure kept |

## Adding a format

Each format lives in its own package under `internal/converter/`. Create a new
one that implements `converter.Converter` and registers itself in an `init`:

```go
// internal/converter/docx/docx.go
package docx

import "github.com/rapatao/md2/internal/converter"

type Converter struct{}

func (Converter) Convert(src []byte, w io.Writer) error { /* ... */ }

func init() { converter.Register("docx", Converter{}) }
```

Then blank-import the package in `main.go` so its `init` runs:

```go
_ "github.com/rapatao/md2/internal/converter/docx"
```

The CLI picks it up automatically — no other changes. It then works standalone
(`-f docx`) and in any comma list (`-f pdf,docx`).

## Layout

```
main.go                       CLI entry: arg parsing, format resolution, I/O
                              (blank-imports each format package)
flags.go                      flag set
main_test.go                  parseFormats + run end-to-end tests
internal/converter/
  converter.go                Converter interface + format registry
  converter_test.go           registry tests
  pdf/pdf.go                  markdown -> PDF: pure-Go first, browser fallback
  html/html.go                markdown -> styled HTML document (+ Render helper)
  text/text.go                markdown -> plain text (AST walker)
  chrome/chrome.go            HTML -> PDF via headless browser (go-rod)
```

## Test

```sh
go test ./...
```

## License

[MIT](LICENSE) © rapatao
