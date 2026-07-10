# md2

[![CI](https://github.com/rapatao/md2/actions/workflows/ci.yml/badge.svg)](https://github.com/rapatao/md2/actions/workflows/ci.yml)

Convert markdown files to other formats. Pure Go by default, extensible to new output formats.

Currently supported:

- **PDF** (`.pdf`) — syntax-highlighted code blocks
- **HTML** (`.html`) — self-contained; local images embedded as data URIs (and remote images too with `-flatten`); syntax-highlighted code blocks. Diagrams render via inlined mermaid.js (or as static images with `-flatten`, e.g. for Google Docs import), or as inline SVG for D2 (rendered in-process, no browser)
- **Plain text** (`.txt`)
- **EPUB** (`.epub`) — EPUB3 ebook (validates with epubcheck). Shares the HTML renderer, so syntax-highlighted code carries over, and the stylesheet has a `prefers-color-scheme: dark` variant for readers' dark mode. Diagrams (Mermaid, D2, PlantUML) are inlined as SVG in a **light and a dark theme**, toggled by the reader's color scheme, so they stay legible in both (Mermaid needs a browser at convert time, like the PDF diagram path). A navigation TOC is built from the document's headings; `dc:title`/`dc:creator` come from `-title`/`-author`. Local images are packaged into the archive

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
md2 -f epub input.md          # writes input.epub (EPUB3 ebook)
md2 -f epub -author "Jane Doe" -title "My Manual" input.md  # epub with metadata
md2 -f pdf,html input.md      # writes input.pdf and input.html
md2 -f html -render mermaid -flatten input.md  # self-contained html, diagrams as images (Google Docs)
md2 -f html -render plantuml input.md          # render plantuml diagrams via a PlantUML server
md2 -f html -css extra.css input.md  # append custom CSS after the built-in stylesheet
md2 -o report.pdf input.md    # explicit output (format from extension)
md2 -f html -stdout input.md  # write html to stdout (no file), e.g. to pipe
md2 -f pdf -o book.pdf intro.md chapter1.md chapter2.md  # merge files, in order, into one document
md2 -f pdf -o manual.pdf docs/  # merge every docs/*.md, sorted, into one document
md2 -f html -per-file docs/     # convert each docs/*.md to its own .html
md2 -f pdf -o book.pdf -recursive docs/  # merge docs/ and its sub-folders
curl -s https://example.com/readme.md | md2 -f html -o out.html -  # read markdown from stdin
```

Pass `-` as the input to read markdown from stdin (the symmetric complement to
`-stdout`). Because `flag` stops parsing at the first non-flag argument, put
`-o`/`-f` **before** the `-`. Stdin has no source directory, so an explicit
`-o` (or `-stdout`) is required, and relative image references resolve against
the working directory.

Flags:

- `-o` output file. Default: the input's name with the format extension. Cannot be combined with multiple formats. **Required when merging multiple inputs** (several files, or a directory) into one document.
- `-f` output format(s), comma-separated. Default: inferred from `-o` extension, else `pdf`. Duplicates are ignored.
- `-render` diagram renderer(s) to enable, comma-separated (`mermaid`, `d2`, `plantuml`), or `all`. Default: none — diagrams render as plain code unless enabled.
- `-flatten` (HTML only) flatten diagrams to static images instead of inlining mermaid.js, **and** fetch remote `http(s)` images and embed them as data URIs, for a fully self-contained file with no JS runtime or external assets needed to view it (e.g. importing into Google Docs). Requires a browser for diagrams, and — new in this flag — **network access at convert time for any document that references remote images** (so a doc with remote images no longer converts in an airgapped/offline environment under `-flatten`). A remote image that can't be fetched is left as a live reference with a warning, not a hard failure.
- `-user-agent` `User-Agent` header sent when `-flatten` fetches remote images to embed. Default: a browser-like string, since some hosts reject the default Go client UA. Override for hosts with specific requirements.
- `-keep-diagram-source` keep the original diagram source in the output in addition to the rendered diagram: the rendered diagram is emitted first, immediately followed by the source as a code block. Default: off — a diagram replaces its source.
- `-plantuml-server` base URL of the PlantUML server used to render `plantuml` diagrams to SVG at build time. Default: the public `https://www.plantuml.com/plantuml`. PlantUML has no pure-Go renderer, so md2 encodes the diagram source and fetches the rendered SVG from this server (inlining it, so the output stays self-contained). This means the diagram source is sent to the server over the network — point it at a self-hosted server for offline or private use.
- `-css` (HTML output and the browser-rendered PDF fallback only — **not** the pure-Go PDF path) path to a CSS file whose contents are appended after the built-in stylesheet, so it can override or extend the defaults via normal CSS cascade rules. Local `@import`s inside it are resolved and inlined recursively (relative to the importing file's directory), so the output stays self-contained; remote `@import url(https://...)`s are left as-is for the browser to fetch. Since the pure-Go PDF renderer has no CSS support, passing `-css` with `-f pdf` forces the headless-browser engine, requiring a browser.
- `-per-file` with multiple inputs (several files, or a directory) convert each to its own output next to its source instead of merging into one document. Cannot be combined with `-o`/`-stdout` (which name a single destination).
- `-recursive` when the input is a directory, also pick up `.md` files in sub-directories, ordered folder by folder (a folder's own files first, then its sub-folders). Default: top-level `*.md` only.
- `-author` (EPUB only) author metadata written as `dc:creator`.
- `-title` (EPUB only) title metadata (`dc:title`); defaults to the document's first heading.
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

```plantuml
Alice -> Bob: hello
```
````

```sh
md2 -f html -render mermaid input.md            # enable mermaid (interactive)
md2 -f html -render mermaid -flatten input.md   # diagrams as static images
md2 -f html -render d2      input.md            # enable D2 (inline SVG)
md2 -f html -render plantuml input.md           # enable PlantUML (server-rendered SVG)
md2 -f pdf  -render all     input.md            # enable every supported renderer
md2 -f html -render mermaid -keep-diagram-source input.md  # rendered diagram + its source
```

Three renderers are supported, with different rendering models:

- **`mermaid`** renders *client-side*. In **HTML**, the
  [mermaid](https://mermaid.js.org) library is inlined into the output (no
  network access needed to view it) and the block renders to SVG in the browser
  — interactive, but needing a JS runtime to display. With `-flatten`, md2
  renders the document in a headless browser and replaces each diagram with a
  static PNG image, producing a self-contained file that displays anywhere —
  including a Google Docs import (upload the `.html` to Drive, then
  "Open with > Google Docs"), which runs no JavaScript. `-flatten` also fetches
  any remote `http(s)` images and embeds them as data URIs (needing network
  access at convert time), so the output has no external asset dependencies at
  all. In **PDF**, a mermaid
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
- **`plantuml`** ([PlantUML](https://plantuml.com)) renders *at conversion time*
  by sending the encoded diagram source to a PlantUML server (public by default,
  or a self-hosted one via `-plantuml-server`) and inlining the returned SVG, so
  the output stays self-contained. PlantUML has no pure-Go renderer, so this is
  the one renderer that reaches over the network — point `-plantuml-server` at a
  private instance for offline or confidential diagrams. In **HTML** the SVG is
  embedded inline (no browser); in **PDF** the block routes through the
  headless-browser engine, as with mermaid and d2, since the pure-Go PDF renderer
  cannot rasterize SVG.

By default a rendered diagram replaces its source. Pass `-keep-diagram-source`
to keep both: the rendered diagram is emitted first, immediately followed by the
original source as a code block.

In **plain text** output, diagrams are not rendered — the source is kept as code.
Files without a diagram are unaffected by any of the above.

The `-render` flag accepts any combination of the supported renderers, or `all`
to enable every one, and is designed to take additional renderers in the future.

### Output naming

When `-o` is omitted, each output keeps the input's path and base name, swapping the
extension for the format — `docs/report.md` with `-f pdf,html` produces
`docs/report.pdf` and `docs/report.html`.

### Multiple inputs

Pass more than one markdown file to merge them, in the order given, into a
single output document. Merging has no obvious output name, so `-o` (or
`-stdout`) is **required**:

```sh
md2 -f pdf -o book.pdf intro.md chapter1.md chapter2.md
```

Flags must come before the input files — Go's `flag` package stops parsing at
the first non-flag argument, so `-o`/`-f` cannot follow the file list. Files
are concatenated with a blank line between them (so the last line of one file
never merges into the first line of the next); heading levels are used as-is,
with no automatic page or section break inserted. Each file's relative image
references resolve against its own directory.

Pass `-per-file` instead to convert each input to its own output next to its
source, rather than merging:

```sh
md2 -f html -per-file intro.md chapter1.md   # writes intro.html and chapter1.html
```

### Directory input

Give a directory as the input to pick up the `.md` files inside it. The same
merge-vs-split rules apply: `-o` merges every file into one document,
`-per-file` converts each to its own output:

```sh
md2 -f pdf -o manual.pdf docs/   # merge all docs/*.md, sorted, into one PDF
md2 -f html -per-file docs/      # one .html per .md, next to each source
```

Files are picked up top-level only by default; add `-recursive` to descend into
sub-directories. They are ordered **folder by folder** — a folder's own files
first (sorted by name), then each sub-folder in turn, recursively — so naming
files `01-intro.md`, `02-setup.md` gives a predictable merge order. A directory
with no `.md` files is an error. The input must be *either* a single directory
*or* a list of files, not a mix.

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
main.go                       thin entry point: resolve version, call cli.Run
internal/cli/
  cli.go                      arg parsing, format/render resolution, I/O orchestration
  flags.go                    flag set definition
internal/converter/
  converter.go                Converter interface + format registry
  pdf/pdf.go                  markdown -> PDF: pure-Go first, browser fallback
  html/html.go                markdown -> styled HTML document (+ Render helper)
  html/d2.go                  D2 diagrams -> inline SVG (in-process, pure Go)
  html/plantuml.go            PlantUML diagrams -> inline SVG (via PlantUML server)
  html/assets/                bundled mermaid.min.js inlined into HTML output
  text/text.go                markdown -> plain text (AST walker)
  chrome/chrome.go            HTML -> PDF / diagram capture via headless browser (go-rod)
internal/css/                 -css load, @import inlining, append to stylesheet
internal/merge/               concatenate multiple input files into one document
internal/urlref/              classify image/link references as local paths vs URLs
internal/consent/             interactive prompt gating the Chromium download
```

Each package has a matching `*_test.go` alongside it.

## Test

```sh
go test ./...
```

## License

[MIT](LICENSE) © rapatao
