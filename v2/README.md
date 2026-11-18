# Carve 0.2 for Go

This package embeds `carve2 call` from a pinned commit on the carve-rs `next2` branch. The root `carve-go` package keeps its Carve 0.1 API. Import `github.com/markup-carve/carve-go/v2` for Carve 0.2. The package is not released yet; pin this repository's `next2` commit with Go's git module support when testing it from another repository.

```go
import carve2 "github.com/markup-carve/carve-go/v2"

html, err := carve2.ToHTML("# Hello")
```

The 0.2 default escapes raw HTML and applies its URL policy. To allow trusted raw HTML, set `Options.RawHTML` explicitly. `Options.URLPolicy` can add an allow or deny list. `Options.Mode` accepts `interactive` or `static`. `Options.Profile`, `HeadingIDs`, and `Symbols` map to the 0.2 option catalog. `Options.Extensions` is retained for future host registration but returns an unavailable error when nonempty because WASI JSON cannot carry host renderers yet.

`RenderWithDiagnostics`, `Parse`, `RenderTree`, `Lint`, `RenderWithIncludes`, `WriteTree`, `Format`, and `ImportDocument` use the public 0.2 JSON calls. `EncodeTree`, `DecodeTree`, `ValidateTree`, and `IncludeDependencies` expose the other tree calls. A caller can use `Call` for another public tooling call without waiting for a typed helper. Tree JSON uses the 0.2 AST schema and diagnostic ranges use UTF-8 byte offsets. Include roots and source paths must be absolute; the WASI guest sees only the mounted root. `WriteTree` accepts `WriteOptions` for format settings. `Write` is a source convenience helper: its HTML target applies render options, while other targets refuse options their writer calls cannot read.

The writer registry currently contains HTML. `ToMarkdown`, `ToPlainText`, `ToANSI`, and `ToCarve` keep their entry points and return a clear unavailable error until those writers register. `FromHTML` reaches the 0.2 importer call, which currently returns unsupported. `FromMarkdown` reports that the 0.2 engine has no Markdown importer. These gaps are tracked in the upstream engine repository.

The embedded bytes are in `internal/wasm/carve2.wasm`. `REV` pins the exact carve-rs `next2` commit, and `carve2.wasm.sha256` records the artifact hash. To rebuild, check out that commit in a clean carve-rs clone and run `CARVE_RS=/path/to/carve-rs ./v2/build-wasm.sh`. The source checkout needs the WASI target and Rust toolchain. Go users need neither Rust nor a WASI runtime outside wazero.
