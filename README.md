# md2

[![CI](https://github.com/rapatao/md2/actions/workflows/ci.yml/badge.svg)](https://github.com/rapatao/md2/actions/workflows/ci.yml)

Convert markdown files to other formats. Pure Go by default, extensible to new output formats.

Currently supported:

- **PDF** (`.pdf`) — syntax-highlighted code blocks
- **HTML** (`.html`) — self-contained; local images embedded as data URIs; syntax-highlighted code blocks. Diagrams render via inlined mermaid.js (or as static images with `-flatten`, e.g. for Google Docs import), or as inline SVG for D2 (rendered in-process, no browser)
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

**Arch Linux** (AUR):

```sh
yay -S md2-bin      # or: paru -S md2-bin
```

**Prebuilt binaries**: download the archive for your OS/arch from the
[latest release](https://github.com/rapatao/md2/releases/latest). Each release
is signed (keyless, via [cosign](https://github.com/sigstore/cosign)) — see
[Verifying a release](#verifying-a-release).

**Linux packages** (`.deb` / `.rpm` / `.apk`): download the package matching
your distro and arch from the
[latest release](https://github.com/rapatao/md2/releases/latest), then install
it:

```sh
sudo dpkg -i md2_*_amd64.deb                       # Debian/Ubuntu
sudo rpm -i md2-*.x86_64.rpm                        # Fedora/RHEL/openSUSE
sudo apk add --allow-untrusted md2_*_x86_64.apk    # Alpine
```

(`--allow-untrusted` because the `.apk` is not signed with an apk repo key.)

**Go**:

```sh
go install github.com/rapatao/md2@latest
```

Or build locally:

```sh
go build -o md2 .
```

**Docker** (multi-arch, `linux/amd64` + `linux/arm64`, published to GHCR):

md2 reads input from file paths and writes output next to the input (or to
`-stdout`), so mount your working directory and run from it:

```sh
docker run --rm -v "$PWD:/work" ghcr.io/rapatao/md2 input.md               # writes input.pdf
docker run --rm -v "$PWD:/work" ghcr.io/rapatao/md2 -f html -stdout input.md > out.html
docker run --rm -v "$PWD:/work" ghcr.io/rapatao/md2 -f html -render mermaid -flatten input.md
```

The default image bundles Chromium, so every feature works — including
browser-fallback PDF, mermaid rendering, and `-flatten`. A smaller `:slim`
variant omits the browser and covers HTML, txt, D2, PlantUML, and pure-Go PDF
only; mermaid, `-flatten`, and the browser PDF fallback are unavailable there:

```sh
docker run --rm -v "$PWD:/work" ghcr.io/rapatao/md2:slim -f html input.md
```

Tags: `latest` and `<version>` (full), `slim` and `<version>-slim` (minimal).

## Verifying a release

Each release publishes `checksums.txt` plus a keyless [cosign](https://github.com/sigstore/cosign)
signature (`checksums.txt.sig`) and certificate (`checksums.txt.pem`), proving
the checksums were produced by the `release.yml` workflow in this repo (not a
tampered fork or mirror).

```sh
cosign verify-blob checksums.txt \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp "https://github.com/rapatao/md2/.github/workflows/release.yml@.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Then confirm your downloaded archive matches a line in the verified `checksums.txt`:

```sh
sha256sum --ignore-missing -c checksums.txt
```

## Usage

```sh
md2 input.md                  # writes input.pdf (default format)
md2 -f html input.md          # writes input.html
md2 -f txt input.md           # writes input.txt (plain text)
md2 -f pdf,html input.md      # writes input.pdf and input.html
md2 -f html -render mermaid -flatten input.md  # self-contained html, diagrams as images (Google Docs)
md2 -f html -render plantuml input.md          # render plantuml diagrams via a PlantUML server
md2 -f html -css extra.css input.md  # append custom CSS after the built-in stylesheet
md2 -o report.pdf input.md    # explicit output (format from extension)
md2 -f html -stdout input.md  # write html to stdout (no file), e.g. to pipe
md2 -f pdf -o book.pdf intro.md chapter1.md chapter2.md  # merge files, in order, into one document
```

Flags:

- `-o` output file. Default: (first) input name with the format extension. Cannot be combined with multiple formats.
- `-f` output format(s), comma-separated. Default: inferred from `-o` extension, else `pdf`. Duplicates are ignored.
- `-render` diagram renderer(s) to enable, comma-separated (`mermaid`, `d2`, `plantuml`), or `all`. Default: none — diagrams render as plain code unless enabled.
- `-flatten` (HTML only) flatten diagrams to static images instead of inlining mermaid.js, for a self-contained file with no JS runtime needed to view it (e.g. importing into Google Docs). Requires a browser.
- `-keep-diagram-source` keep the original diagram source in the output in addition to the rendered diagram: the rendered diagram is emitted first, immediately followed by the source as a code block. Default: off — a diagram replaces its source.
- `-plantuml-server` base URL of the PlantUML server used to render `plantuml` diagrams to SVG at build time. Default: the public `https://www.plantuml.com/plantuml`. PlantUML has no pure-Go renderer, so md2 encodes the diagram source and fetches the rendered SVG from this server (inlining it, so the output stays self-contained). This means the diagram source is sent to the server over the network — point it at a self-hosted server for offline or private use.
- `-css` (HTML output and the browser-rendered PDF fallback only — **not** the pure-Go PDF path) path to a CSS file whose contents are appended after the built-in stylesheet, so it can override or extend the defaults via normal CSS cascade rules. Local `@import`s inside it are resolved and inlined recursively (relative to the importing file's directory), so the output stays self-contained; remote `@import url(https://...)`s are left as-is for the browser to fetch. Since the pure-Go PDF renderer has no CSS support, passing `-css` with `-f pdf` forces the headless-browser engine, requiring a browser.
- `-stdout` write the converted result to standard output instead of a file, for piping into other tools. Single format only. With `-o` it also writes the file.
- `-allow-download` authorize downloading Chromium for the browser renderer without prompting (useful in CI).
- `-version` print the version and exit.

## PDF engine

PDF uses a two-stage strategy:

1. **Pure Go** (`goldmark-pdf`) — fast, no external runtime. Handles most
   documents, but has no HTML/CSS layer, so `-css` has no effect on it.
2. **Headless browser fallback** — if the pure-Go renderer fails (e.g. complex
   tables, or glyphs like emoji it cannot lay out), or if `-css` is passed
   (custom CSS can only be applied to a rendered HTML document), md2 prints a
   styled HTML version to PDF with Chrome/Chromium for full fidelity.

The fallback prefers a browser already installed on the system. If none is
found it asks before downloading Chromium (~150MB, cached for later runs):

```
No Chrome/Chromium found. Download Chromium (~150MB) to render the PDF? [y/N]:
```

On a non-interactive terminal it declines unless `-allow-download` is passed.
Simple documents never launch a browser.

## Syntax highlighting

Fenced code blocks with a language tag (e.g. ` ```go `, ` ```js `) are
syntax-highlighted automatically — no flag needed — using
[chroma](https://github.com/alecthomas/chroma) with the light `github` theme.
Highlighting is applied to **HTML** (colors inlined as a self-contained
stylesheet) and to **PDF** (both the pure-Go and browser-rendered engines).
Blocks with no language, or a language chroma does not recognize, are left as
plain code.

Because the HTML colors are emitted as CSS classes, `-css` can recolor tokens
via the cascade — e.g. `.chroma .k { color: #b00 }` restyles keywords.

## Diagrams

Diagram rendering is **off by default** and enabled per run with `-render`.
Without it, a diagram code block is rendered as plain code.

````markdown
```mermaid
graph TD; A-->B;
```

```d2
x -> y
```
````

```sh
md2 -f html -render mermaid input.md            # enable mermaid (interactive)
md2 -f html -render mermaid -flatten input.md   # diagrams as static images
md2 -f html -render d2      input.md            # enable D2 (inline SVG)
md2 -f pdf  -render all     input.md            # enable every supported renderer
md2 -f html -render mermaid -keep-diagram-source input.md  # rendered diagram + its source
```

Two renderers are supported, with different rendering models:

- **`mermaid`** renders *client-side*. In **HTML**, the
  [mermaid](https://mermaid.js.org) library is inlined into the output (no
  network access needed to view it) and the block renders to SVG in the browser
  — interactive, but needing a JS runtime to display. With `-flatten`, md2
  renders the document in a headless browser and replaces each diagram with a
  static PNG image, producing a self-contained file that displays anywhere —
  including a Google Docs import (upload the `.html` to Drive, then
  "Open with > Google Docs"), which runs no JavaScript. In **PDF**, a mermaid
  block forces the headless-browser engine (the pure-Go renderer cannot run
  JavaScript), so the diagram is captured as vector graphics. Inlining the
  library adds ~3 MB to each HTML file that contains a mermaid diagram;
  `-flatten` avoids that.
- **`d2`** ([D2](https://d2lang.com)) renders *at conversion time*, in-process
  via the pure-Go D2 library — no JS runtime, no external binary. In **HTML** the
  resulting SVG is embedded inline, so the output is self-contained and needs no
  browser (no `-flatten`). In **PDF**, since the pure-Go PDF renderer cannot
  rasterize SVG, a d2 block routes through the headless-browser engine like
  mermaid. A d2 block whose source fails to compile is left as a plain code block
  (with a warning on stderr) rather than failing the conversion.

By default a rendered diagram replaces its source. Pass `-keep-diagram-source`
to keep both: the rendered diagram is emitted first, immediately followed by the
original source as a code block.

In **plain text** output, diagrams are not rendered — the source is kept as code.
Files without a diagram are unaffected by any of the above.

The `-render` flag is designed to take additional renderers (e.g. `plantuml`) in
the future.

### Output naming

When `-o` is omitted, each output keeps the input's path and base name, swapping the
extension for the format — `docs/report.md` with `-f pdf,html` produces
`docs/report.pdf` and `docs/report.html`.

### Multiple inputs

Pass more than one markdown file to merge them, in the order given, into a
single output document:

```sh
md2 -f pdf -o book.pdf intro.md chapter1.md chapter2.md
```

Flags must come before the input files — Go's `flag` package stops parsing at
the first non-flag argument, so `-o`/`-f` cannot follow the file list. Files
are concatenated with a blank line between them (so the last line of one file
never merges into the first line of the next); heading levels are used as-is,
with no automatic page or section break inserted. Each file's relative image
references resolve against its own directory. When `-o` is omitted, the
merged output takes the *first* input's base name.

## Supported formats

| Format | Extension | Engine |
|--------|-----------|--------|
| `pdf`  | `.pdf`    | goldmark-pdf (pure Go), browser fallback (go-rod) |
| `html` | `.html`   | goldmark (GFM), styled standalone document; local images embedded as data URIs; diagrams as mermaid.js (or, with `-flatten`, static PNGs via go-rod) or in-process D2 inline SVG |
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
