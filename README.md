# go-carve

A pure-Go module that renders [Carve](https://markup-carve.github.io/carve/)
markup to HTML.

It embeds a WASI (`wasm32-wasip1`) build of the reference Carve engine
([carve-rs](https://github.com/markup-carve/carve-rs)) and runs it with the
[wazero](https://github.com/tetratelabs/wazero) runtime. There is **no cgo** and
**no JavaScript host** involved: the engine is driven over the WASI stdio
contract (Carve source on stdin, HTML on stdout). The Go output is therefore
byte-for-byte the output of the engine it wraps.

This fills the Go gap for Carve and is the prerequisite for a future Hugo
integration.

## Install

```bash
go get github.com/markup-carve/carve-go
```

## Usage

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

### API

```go
// ToHTML renders Carve source to HTML using the embedded engine.
// Safe to call concurrently from multiple goroutines.
func ToHTML(source string) (string, error)

// ToHTMLContext is ToHTML with a caller-supplied context that bounds
// wasm compilation (first call) and per-call execution.
func ToHTMLContext(ctx context.Context, source string) (string, error)
```

Carve inline conventions (note these differ from Markdown):

- `*x*` renders as `<strong>x</strong>` (bold)
- `/x/` renders as `<em>x</em>` (italic)

## How it works

- The wasm module is compiled **once** (lazily, on first call) and cached for
  the lifetime of the process.
- Each call instantiates a **fresh** module instance with isolated stdio, so
  per-call state never leaks and concurrent calls are safe.
- wazero's `wasi_snapshot_preview1` host functions satisfy the engine's WASI
  imports (`fd_read`, `fd_write`, `proc_exit`, ...). The Go side wires
  `stdin = source` and captures `stdout` into a buffer, runs `_start`, and
  returns the captured output.

## How the embedded `.wasm` is built

The embedded artifact at `internal/wasm/carve.wasm` is the carve-rs CLI
compiled to `wasm32-wasip1`. That CLI already implements the exact contract this
module needs:

- reads Carve source from **stdin** when no file argument is given,
- writes rendered **HTML to stdout** (the default `--html` format),
- appends a single trailing newline if the output lacks one.

Because the existing CLI already does stdin to HTML stdout, **no wrapper crate
is needed**. Regenerate the artifact with:

```bash
CARVE_RS=/path/to/carve-rs ./build-wasm.sh
```

which runs, in effect:

```bash
rustup target add wasm32-wasip1
cd "$CARVE_RS"
cargo build --release --target wasm32-wasip1 --bin carve
cp target/wasm32-wasip1/release/carve.wasm \
   /path/to/go-carve/internal/wasm/carve.wasm
```

The `internal/wasm/carve.wasm` file is **committed** to the repository: it is
the shipped artifact. The `.gitignore` deliberately does not ignore it.

### Pinning the engine version when publishing

During local development `build-wasm.sh` uses a local checkout of carve-rs (a
path dependency). For a published, reproducible build, pin a specific carve-rs
revision. For example, clone a tagged release and point `CARVE_RS` at it:

```bash
git clone --branch v0.1.0 https://github.com/markup-carve/carve-rs /tmp/carve-rs
CARVE_RS=/tmp/carve-rs ./build-wasm.sh
```

Record the carve-rs commit/tag used to generate the committed `.wasm` in your
release notes so the artifact is reproducible.

## Testing

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

The test suite asserts headings, Carve bold (`*x*`), Carve emphasis (`/x/`),
lists, links, and tables; that empty input does not panic; that concurrent calls
are safe (under `-race`); and that `ToHTML` output is byte-identical to the
native carve-rs CLI on several samples (normalizing a single trailing newline).
The byte-identical test auto-skips if the native `carve` binary is not found;
set `CARVE_BIN=/path/to/carve` to point it explicitly.

## License

MIT. See [LICENSE](LICENSE).
