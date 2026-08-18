# Changelog

Notable changes to `github.com/markup-carve/carve-go`.

The parser and renderer are carve-rs, compiled to `wasm32-wasip1` and committed
as `internal/wasm/carve.wasm`; `internal/wasm/REV` records which carve-rs commit
produced those bytes. An engine rebuild can therefore change rendering without a
line of Go changing, so rebuilds get an entry of their own.

## [Unreleased]

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
