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
// ToHTML renders Carve source to HTML (interactive default).
// Safe to call concurrently from multiple goroutines.
func ToHTML(source string) (string, error)

// ToHTMLContext is ToHTML with a caller-supplied context that bounds
// wasm compilation (first call) and per-call execution.
func ToHTMLContext(ctx context.Context, source string) (string, error)

// ToHTMLStatic renders self-contained static HTML: it flattens interactive
// constructs and degrades diagrams/math to source (see "Static render mode").
func ToHTMLStatic(source string) (string, error)

// ToHTMLOptions renders with explicit options. The zero Options value equals
// ToHTML (interactive, no extensions).
func ToHTMLOptions(source string, opts Options) (string, error)
func ToHTMLOptionsContext(ctx context.Context, source string, opts Options) (string, error)

// Options configures a render call. The zero value is the interactive default.
type Options struct {
	Static     bool     // self-contained static HTML (CLI --static; implies --extensions)
	Extensions []string // enable bundled interactive extensions (CLI --extensions)
}
```

Carve inline conventions (note these differ from Markdown):

- `*x*` renders as `<strong>x</strong>` (bold)
- `/x/` renders as `<em>x</em>` (italic)

## Resource limits and untrusted input

The embedded engine runs in the wazero wasm runtime, which is hardened so a
single call cannot run away with host CPU:

- **Per-call cancellation.** The runtime is built with
  `WithCloseOnContextDone`, so the `context.Context` you pass to
  `ToHTMLContext` / `ToHTMLOptionsContext` genuinely interrupts CPU-bound parse
  loops. An expired deadline or canceled context returns promptly with an error
  that satisfies `errors.Is(err, context.DeadlineExceeded)` /
  `context.Canceled`, instead of letting the input run to completion.

  > [!IMPORTANT]
  > For **untrusted input**, always use `ToHTMLContext` (or
  > `ToHTMLOptionsContext`) with a deadline. The plain `ToHTML` /
  > `ToHTMLStatic` / `ToHTMLOptions` helpers use `context.Background()` and are
  > therefore **unbounded** in time. Some pathological inputs are processed in
  > super-linear time by the engine, so without a deadline a single small
  > adversarial document can occupy a goroutine for many seconds.

  ```go
  ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
  defer cancel()
  html, err := carve.ToHTMLContext(ctx, untrusted)
  if errors.Is(err, context.DeadlineExceeded) {
      // input exceeded the render budget; reject it
  }
  ```

## Static render mode

`ToHTMLStatic` (or `ToHTMLOptions` with `Options{Static: true}`) produces
self-contained HTML that is safe to publish without a JavaScript client. It
maps to the engine CLI flags `--html --static --extensions` (`Static` implies
`--extensions`, since that is what produces the constructs to flatten) and:

- flattens interactive constructs - a collapsed `<details>` becomes
  `<details open>`, and spoilers are revealed
  (`<span class="spoiler spoiler-revealed">`);
- degrades diagram and math fences (mermaid, chart, graphviz, math) to their
  **source** as a `<pre><code class="language-...">` block.

```go
html, err := carve.ToHTMLStatic("::: details \"More\"\nBody.\n:::")
// -> <details open>...</details>
```

### Limitation: no build-time image renderers (partial rollout)

> [!IMPORTANT]
> carve-go static mode is **flatten + source fallback only**. Build-time
> renderer injection (turning a mermaid/math fence into a rendered image or
> server-side MathML) is **not supported** in carve-go.

The sibling in-process engines (carve-js, carve-php, carve-py, carve-rb)
accept host closures that the static renderer calls to inject `<svg>` / `<img>`
/ MathML at build time. carve-go embeds the engine as a `wasm32-wasip1` CLI and
drives it over the WASI stdio boundary, so there is no way to pass a Go closure
into the engine. Diagrams and math therefore always degrade to their source in
carve-go.

If you need rendered images, pre-render the diagrams yourself, or use one of
the in-process engines for the static build step.

This is the intentional partial entry in the graceful-degradation set
(spec carve #205; siblings carve-js #242, carve-php #240, carve-rs #143,
carve-py #1, carve-rb #1).

> [!NOTE]
> carve-rs - the embedded engine - ships Details, Spoiler, FencedRender
> (mermaid / chart / graphviz) and MathBlock, but **not** a Tabs / CodeGroup
> extension (those are carve-js / carve-php only). So tab/code-group flattening
> is not part of carve-go's static behavior; spoiler reveal and `details`
> opening are the interactive-flatten cases this engine actually covers.

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
- appends a single trailing newline if the output lacks one,
- accepts `--static` and `--extensions` for the static render mode above.

The committed `.wasm` is built from carve-rs branch `main` (static render mode
merged via PR #143), commit
`f7b3658746f4f0d1a58cd1ce3fa22a153b07cbfd`.

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

For static mode it asserts `<details open>` (vs interactive `<details>`),
spoiler reveal, mermaid degrading to a `<pre><code>` source block, that static
and interactive output differ, that the zero `Options` value is unchanged from
`ToHTML`, concurrency safety, and that `ToHTMLStatic` is byte-identical to the
native CLI run with `--html --static --extensions`.

The byte-identical tests auto-skip if the native `carve` binary is not found
(the static one also skips unless the binary advertises `--static`); set
`CARVE_BIN=/path/to/carve` to point it explicitly.

## License

MIT. See [LICENSE](LICENSE).
