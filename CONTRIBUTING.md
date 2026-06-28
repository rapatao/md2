# Contributing to md2

Thanks for your interest in contributing! This guide covers the essentials.

## Getting started

```sh
git clone https://github.com/rapatao/md2
cd md2
make build      # build into bin/
make check      # go fmt + go vet + go test ./...
```

Requires Go 1.26+. The PDF browser fallback uses an installed Chrome/Chromium;
unit tests do not launch a browser.

## Development workflow

1. Open an issue first for non-trivial changes, so we can agree on direction.
2. Branch off `main`.
3. Make your change with tests.
4. Run `make check` — it must pass.
5. Open a pull request and fill in the template.

## Coding guidelines

- Keep the code idiomatic Go; match the style of the surrounding code.
- Every behavior change needs a test. Keep tests hermetic (no network, no
  browser launch).
- Run `go fmt` (or `make fmt`) before committing.

## Adding a new output format

Formats are self-contained and auto-discovered:

1. Create a package under `internal/converter/<format>/`.
2. Implement `converter.Converter` and register it in an `init`:

   ```go
   func init() { converter.Register("<format>", Converter{}) }
   ```

3. Blank-import the package in `main.go` so its `init` runs.
4. Add tests for the renderer.

No changes to the CLI argument handling are needed — it picks the format up
automatically.

## Commit messages

Use clear, imperative messages (e.g. "Add EPUB output format"). Conventional
Commits are welcome but not required.

## Reporting bugs and requesting features

Use the issue templates. Include the command, a minimal markdown input, and any
output or stack trace.
