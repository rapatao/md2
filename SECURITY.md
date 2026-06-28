# Security Policy

## Supported versions

md2 is under active development. Security fixes target the latest `main` and the
most recent release.

## Reporting a vulnerability

Open a public issue using the bug report template, or start a discussion. For
this project, security problems may be reported publicly — no private channel is
required.

Include:

- a description of the issue and its impact,
- steps to reproduce or a proof of concept,
- affected version or commit.

## Scope notes

md2 renders untrusted markdown. The PDF browser fallback launches a local
headless Chrome/Chromium to print HTML; reports about sandbox escapes, SSRF via
embedded resources, or remote-content fetching are welcome.
