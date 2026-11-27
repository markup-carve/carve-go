# Go binding migration to Carve 0.2

From 0.2 on, the root package `github.com/markup-carve/carve-go` is the Carve 0.2 binding. The 0.1 binding stays on the `0.1` branch and its `v0.1.x` tags. During the 0.2 preview the same API lived in the `v2` subpackage; replace that import path with the root path. The package embeds the `carve call` WASI binary (`carve.wasm`) built from the carve-rs commit in `internal/wasm/REV`.

## Render and options

| 0.1 | 0.2 |
| --- | --- |
| `ToHTML`, `ToHTMLOptions` | Same helper names, backed by `renderWithDiagnostics` |
| `Render(source, target, opts)` | Same call shape and named writer targets |
| `Options.Safe: true` | The 0.2 default has `rawHtml: false` and a denying URL policy. Use `Options.RawHTML: true` only for trusted source. |
| `Options.Static: true` | `Options.Mode: "static"` |
| `Options.Profile` | Same field, validated by the 0.2 engine |
| `Options.Symbols` | Same field, sent as the 0.2 `symbols` map |
| `Options.Extensions` | Names the standard extensions in `StandardExtensions` (C2-X-039). `Options.Renderers` maps one to `"standard"`. The 0.2 engine provides the navigation extensions, including heading numbers, permalinks, table of contents, external links, and autolink. |
| `Options.AlignTables` | `Format(ctx, source, true)` |
| `RenderChecked` and loss report | `RenderWithDiagnostics`; losses are diagnostic entries with `channel: "render-loss"` and UTF-8 byte ranges. Set `Options.StrictLosses` to refuse losses. |

`Options.URLPolicy` selects an allow or deny list of URL schemes. It does not weaken the secure default unless the caller explicitly supplies a policy. `HeadingIDs`, `Roundtrip`, and `SourceLines` map to the names in the 0.2 option catalog. The JSON boundary validates enum values and rule names. `Call` exposes other public 0.2 tooling calls while typed helpers are added.

## Tree, lint, includes, and imports

| 0.1 | 0.2 |
| --- | --- |
| `ParseAST` | Same helper, returning a 2.0 tree; `Parse` also returns diagnostics. `RenderTree`, `EncodeTree`, `DecodeTree`, and `ValidateTree` operate on that tree. Positions and diagnostic ranges are UTF-8 byte offsets. |
| `LintWithOptions` with `Bundle` and `CaptionHints` | `Lint(ctx, source, LintOptions{Rules, Platforms, Include})`. Rules use the 0.2 rule catalog and values `on` or `off`. |
| `RenderWithIncludes(source, target, opts, inc)` | Same call shape, with `RenderWithIncludesContext` for cancellation. `Include` still carries an absolute root and source path. The result has diagnostics and dependencies. It also keeps `Warnings` and `SuppressedWarnings` for Hugo callers. Each dependency has the 0.2 `Read` flag, the familiar `Resolved` alias, and a `HostPath` for a read file. `IncludeDependencies` reads the raw 0.2 dependencies from a tree. |
| `FromHTML` | Same entry point, returning the 0.2 `importDocument` record. |
| `FromMarkdown` | Same entry point, returning the 0.2 `importDocument` record for GitHub Flavored Markdown. Markdown import is outside the 0.2 specification (C2-T-003). |
| `DecisionTrace`, stamp calls, and `SelectIncludeRegion` | No 0.2 counterpart. The 0.2 upgrade tool in carve-js handles source migration. |

`RenderWithIncludes` and `Lint` expand includes inside the mounted root. A 0.2 diagnostic from an included file names its `File` and byte `Range`; the 0.1 `IncludedBy` chain has no 0.2 counterpart. Includes expand with `Options.Extensions` set as well.

## Building the pinned engine

In a clean carve-rs checkout at the recorded commit, with `wasm32-wasip1` installed:

```sh
CARVE_RS=/path/to/carve-rs ./build-wasm.sh
```

The script writes the WASI binary, the exact revision, and its checksum together. CI checks that the revision is an ancestor of carve-rs `main` and that the artifact matches its checksum. The binary is embedded, so a user of the Go package needs no Rust toolchain.
