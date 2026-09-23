# carve-go

A pure-Go module that renders [Carve](https://markup-carve.github.io/carve/)
with the reference [carve-rs](https://github.com/markup-carve/carve-rs) engine.
It embeds the engine as `wasm32-wasip1` and runs it with wazero, so callers need
neither cgo nor a JavaScript host.

## Install

```bash
go get github.com/markup-carve/carve-go
```

## Render Carve

```go
package main

import (
	"fmt"

	carve "github.com/markup-carve/carve-go"
)

func main() {
	html, err := carve.ToHTML("# Hello\n\nSome *bold* and /italic/ text.")
	if err != nil {
		panic(err)
	}
	fmt.Print(html)
}
```

Calls are safe across goroutines. Context variants such as `ToHTMLContext`
allow cancellation and deadlines for each render.

The package also provides `ToMarkdown`, `ToPlainText`, `ToANSI`, and `ToCarve`.
`Render` selects a target dynamically, while `ToHTMLOptions` configures HTML
extensions, static output, safe mode, profiles, and symbols.

```go
html, err := carve.ToHTMLOptions(source, carve.Options{
	Safe:       true,
	Profile:    "comment",
	Extensions: []string{"all"},
})
```

## Migration and AST access

`FromHTML` and `FromMarkdown` return canonical Carve with a structured fidelity
report. `ParseAST` exposes the shared serialized AST, and static rendering
produces self-contained HTML without interactive diagram or math dependencies.

The [API reference](docs/reference.md#usage) documents the main functions,
options, output targets, AST access, and migration-report rules.

## Untrusted input

Use a caller-supplied context to bound execution, `Safe: true` to escape raw
HTML, and a restrictive profile to cap document size and filter constructs.
Profiles reject the render when an input exceeds its length cap.

Symbol values are trusted configuration. Do not populate them from user input.
See the [resource and content safety reference](docs/reference.md#resource-limits-and-untrusted-input)
for the complete boundary.

## Includes

`RenderWithIncludes` and its context variant expand includes under a configured
root, track dependencies, and refuse traversal outside that root. String-only
render functions leave include directives literal. Treat inclusion as a
feature for document trees you control, not for untrusted input.

See [File inclusion](docs/reference.md#file-inclusion) for containment,
budgets, dependency identities, warnings, and symlink handling.

## Engine provenance

The module pins a specific carve-rs WASM artifact in `internal/wasm/REV` and
records its digest beside the embedded bytes. CI verifies both values.

## Development

Contributor setup, engine refreshes, tests, and release checks are in the
[development guide](docs/development.md).
