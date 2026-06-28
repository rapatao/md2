# md2

Convert markdown files to other formats. Pure Go by default, extensible to new output formats.

Currently supported:

- **PDF** (`.pdf`)
- **HTML** (`.html`)
- **Plain text** (`.txt`)

## Install

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
md2 -o report.pdf input.md    # explicit output (format from extension)
```

Flags:

- `-o` output file. Default: input name with the format extension. Cannot be combined with multiple formats.
- `-f` output format(s), comma-separated. Default: inferred from `-o` extension, else `pdf`. Duplicates are ignored.
- `-allow-download` authorize downloading Chromium for the PDF browser fallback without prompting (useful in CI).

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

### Output naming

When `-o` is omitted, each output keeps the input's path and base name, swapping the
extension for the format — `docs/report.md` with `-f pdf,html` produces
`docs/report.pdf` and `docs/report.html`.

## Supported formats

| Format | Extension | Engine |
|--------|-----------|--------|
| `pdf`  | `.pdf`    | goldmark-pdf (pure Go), browser fallback (go-rod) |
| `html` | `.html`   | goldmark (GFM), styled standalone document |
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
