# Changelog

Notable changes to `github.com/markup-carve/carve-go`.

The parser and renderer are carve-rs, compiled to `wasm32-wasip1` and committed
as `internal/wasm/carve.wasm`; `internal/wasm/REV` records which carve-rs commit
produced those bytes. An engine rebuild can therefore change rendering without a
line of Go changing, so rebuilds get an entry of their own.

## [Unreleased]

### Changed

- The embedded engine moves from carve-rs 0.1.6 to 0.1.8, two releases and 396
  commits of engine work. Rendering changes: measured against the spec commit
  0.1.8 pins, the previous artifact rendered 204 of 2225 mandatory corpus
  documents differently from the spec and the rebuilt one renders 0. Most of the
  movement is container and column reading (fence and colon closers measured
  from their authored base, comments and definitions folding under a term,
  flush-left lines below a nested item), plus exact-case name lookup for
  cross-references, glossary terms and include selectors, and table row-group
  metadata.

### Added

- `internal/wasm/ENGINE_VERSION` records the version the embedded artifact
  reports for itself, and `TestTheEmbeddedEngineReportsTheRecordedVersion`
  holds the bytes to it. The engine gained `carve --version` in carve-rs 0.1.8,
  which is the first thing carried inside the binary that names which engine it
  is; `internal/wasm/REV` is text beside the artifact and the digest proves only
  that the bytes are intact.

## [0.1.4] - 2026-10-02

### Fixed

- The embedded `internal/wasm/carve.wasm` no longer carries the build host's
  filesystem paths. Up to 0.1.3 it held 96 strings naming one developer's home
  directory and cargo registry, because Rust records the source path of every
  file a panic can fire in and a release build keeps them, so every install
  carried that layout inside the WebAssembly it imports (#70). The artifact was
  rebuilt under `--remap-path-prefix`, from the carve-rs revision
  `internal/wasm/REV` already named, so the engine did not move: those 96
  strings read `/cargo/registry`, and the 1740 mandatory corpus documents still
  render byte-identically. `TestNoHostPathsInTrackedBinaries` scans tracked
  binaries whole, which is the arm a line-based guard could not have, and fails
  the suite on the next one.

- `build-wasm.sh` resolves its carve-rs checkout from the script's own location
  instead of an absolute path that existed on one machine (#69). The script as
  shipped now runs wherever a carve-rs checkout sits beside this one, so a
  reader can rebuild the committed artifact and compare it against its source;
  when neither that sibling nor `CARVE_RS` resolves, the error names both
  locations it tried and carries the clone recipe.

## [0.1.3] - 2026-09-19

### Added

- `RenderWithIncludes` expands `{{ path }}` directives against a caller-supplied
  containment root, returning the rendered output, the per-directive warnings and
  the dependency list a host watches for rebuilds (markup-carve/carve-go#64).
  Every other entry point still leaves directives literal.

### Changed

- Engine rebuilt from carve-rs `2e9c43f2` to released 0.1.6 (`d7837249`). The
  first half of that range is where the include pass lives, and it changed 41 of
  1695 corpus documents, all of them from wrong to right. The second half brings
  the 0.1.6 writer and parser fixes: the Markdown and Carve writers escape what
  would reopen a construct on the way back in, and parsing tightens around
  braced inlines, forced closers, escaped markers, adjacent links, blank table
  rows and a code span's closer. The artifact is byte-identical on the whole
  corpus at the spec it pins, 1740 of 1740 documents.

- **Breaking:** A substitution node in the tree carries `old` and `new` as
  arrays of inline nodes, where it carried the strings `oldText` and `newText`.
  A caller reading that node walks the halves instead of reading them
  (markup-carve/carve-rs#1756).

- **Breaking:** Migration reports use schema version 2 and classify importer outcomes as
  preserved, normalized, degraded, or dropped with explicit confidence. Opaque
  `raw-preserved` HTML is degraded, truncated reports and unknown future codes
  fail closed as dropped/fallback, and the stale `structure-split` code is gone.
- Markdown reports explicitly mark construct-level fidelity as unverified
  on every import, using dropped/fallback as a conservative worst-case gate,
  until the embedded engine exposes native Markdown diagnostics. This replaces
  the previous empty diagnostics array.

## [0.1.2] - 2026-08-27

### Fixed

- An over-cap document under a `Profile` is an **error**, not an empty render
  reported as success (#56, markup-carve/carve-rs#1194). `Profile: "comment"`
  above 100,000 bytes and `Profile: "minimal"` above 10,000 returned `("", nil)`;
  they now return the engine's `max_length_exceeded` line, naming the limit and
  the size given. The refusal reaches every render target, `--carve` included
  (markup-carve/carve-rs#1198).

### Changed

- Rebuild the embedded engine from carve-rs `a33c42a` onto released carve-rs
  0.1.4 (`2e9c43f2`), 250 commits. Beyond the refusal
  above, the rendering changes an existing document can see are carve-rs' own
  `Unreleased` section - among them: `=>` is no longer an arrow and `<==` is the
  canonical left double arrow, a hyphen run opening a word is a flag rather than
  a dash, a table cell's marker run must be followed by a space, `<thead>` and
  `<tfoot>` write one row per line, and diagrams, tab sets, code groups and the
  footnote/index back-links carry accessible names (PART 9 §16a). The final
  release rebuild also includes typed citation items, published block-image
  promotion, the `{empty}` spelling for an empty definition body, and the
  definition/footnote/list continuation-column fixes from carve-rs 0.1.4.
- The embedded artifact renders all 1538 mandatory documents at the spec
  commit carve-rs 0.1.4 pins byte-identically, including the AST vocabulary and
  schema-field checks.

### Added

- `FromHTML` and `FromMarkdown`, with context variants, expose canonical
  migration and a shared machine-readable loss report.
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

[Unreleased]: https://github.com/markup-carve/carve-go/compare/v0.1.4...HEAD
[0.1.4]: https://github.com/markup-carve/carve-go/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/markup-carve/carve-go/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/markup-carve/carve-go/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/markup-carve/carve-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/markup-carve/carve-go/releases/tag/v0.1.0
