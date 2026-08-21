# Changelog

Notable changes to `github.com/markup-carve/carve-go`.

The parser and renderer are carve-rs, compiled to `wasm32-wasip1` and committed
as `internal/wasm/carve.wasm`; `internal/wasm/REV` records which carve-rs commit
produced those bytes. An engine rebuild can therefore change rendering without a
line of Go changing, so rebuilds get an entry of their own.

## [Unreleased]

### Added

- **Markdown, plain-text, ANSI and canonical-Carve output** - `ToMarkdown`,
  `ToPlainText`, `ToANSI`, `ToCarve`, each with a `Context` variant, plus
  `Render`/`RenderContext` and the `OutputFormat` type for a non-HTML format
  with options. The embedded engine already understood every one of these
  flags, so this needs no rebuild and no new dependency; `OutputHTML` is the
  zero value, so existing callers are unaffected. `ToCarve` is the formatter:
  it returns what `carve fmt` writes, and it is idempotent.

  `Options.Static` is rejected for non-HTML formats rather than ignored.
  `Options.Symbols` reaches HTML only - an engine limitation, pinned by a test
  and documented in the README.

- `Options.Symbols` renders `:name:` shortcodes, mapping each entry to the
  engine's repeatable `--symbol NAME=VALUE` (#52, #53). Keys are sorted, because
  Go randomizes map iteration and an unsorted range built a different command
  line on every call for the same map. An empty name, a name containing `=` and
  a NUL in either half are refused rather than corrupted; anything the engine's
  shortcode grammar cannot match is inert, not an error. Values are substituted
  raw, so the map is trusted configuration and must never be built from user
  input.

## [0.1.1] - 2026-08-18

### Security

- A list-valued URL attribute is probed at every candidate, not at its head
  (PART 9 §25, markup-carve/carve#1320). The sanitizer read only the leading
  scheme of the value, so `srcset="safe.png 1x, javascript:alert(1) 2x"` passed
  on its second entry; `srcset`, `imagesrcset`, `ping` and `attributionsrc` are now
  split and every candidate is read. The engine embedded in `v0.1.0` predates
  the fix, so the module published so far carries the defect.

### Changed

- Rebuild the embedded engine from carve-rs `1d788a73` onto carve-rs `0.1.3`
  (`a33c42ade077467733435322a66cce7957cd491c`), 163 commits. The rendering
  changes an existing document can see are carve-rs' own `0.1.3` changelog
  section.
- The spec corpus this artifact renders byte-identically is 1259 documents.

## [0.1.0] - 2026-08-10

First release. `ToHTML` and the AST surface over a carve-rs engine embedded as
WebAssembly and driven with wazero, so the module has no cgo and no external
process.

[Unreleased]: https://github.com/markup-carve/carve-go/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/markup-carve/carve-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/markup-carve/carve-go/releases/tag/v0.1.0
