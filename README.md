# go-carve

A pure-Go module that renders [Carve](https://markup-carve.github.io/carve/)
markup to HTML.

It embeds a WASI (`wasm32-wasip1`) build of the reference Carve engine
([carve-rs](https://github.com/markup-carve/carve-rs)) and runs it with the
[wazero](https://github.com/tetratelabs/wazero) runtime. There is **no cgo** and
**no JavaScript host** involved: the engine is driven over the WASI stdio
contract (Carve source on stdin, HTML on stdout). The Go output is therefore
byte-for-byte the output of the engine it wraps.

This fills the Go gap for Carve and powers [hugo-carve](https://github.com/markup-carve/hugo-carve),
a preprocessor that renders Carve content to HTML for Hugo (stock Hugo cannot
load a custom Go renderer, so hugo-carve uses this module as a library rather
than running in-process).

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
// per-call execution (a deadline/cancellation interrupts the render). The
// one-time wasm compilation runs under a background context.
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
	Safe       bool     // escape =html raw blocks/spans (CLI --safe)
	Profile    string   // full|article|comment|minimal (CLI --profile)
}

// ReadStamp reports the provenance marker a document carries; ok is false when
// it carries none. NeedsReview reports whether the document was last processed
// under an older spec version than the embedded engine targets.
func ReadStamp(source string) (Stamp, bool, error)
func ReadStampContext(ctx context.Context, source string) (Stamp, bool, error)
func NeedsReview(source string) (bool, error)
func NeedsReviewContext(ctx context.Context, source string) (bool, error)

type Stamp struct {
	Version     string // the spec version the document was last processed under
	GeneratedBy string // the engine that wrote the marker, empty when unrecorded
}
```

### The parsed AST

`ParseAST` returns the document as JSON - the [PART 12 exchange
shape](https://markup-carve.github.io/carve/ast-json), the same tree every Carve
engine publishes, so a consumer written against one implementation reads
another's output.

```go
raw, err := carve.ParseAST("# Title\n\nBody[^a].\n\n[^a]: note\n")
// raw is json.RawMessage:
// {"type":"document","children":[{"type":"heading",...}],"srcByteLength":34}
```

The root carries exactly `type`, `children` and `srcByteLength`; frontmatter and
footnote definitions are block nodes inside `children`, not root fields. Every
node except the root carries `pos` when the engine could place it - 1-based
lines and columns, 0-based offsets, ends exclusive, counted in Unicode
**codepoints**, not bytes. A node the engine could not place, such as
reassembled table-cell text, carries no `pos` at all rather than an invented
one.

`json.RawMessage` rather than a typed tree on purpose: the node set is spec
surface that grows, and a Go struct hierarchy would either lag it or force a
breaking change every time it does. Unmarshal into whatever shape you need.

## Stored documents and spec versions

`carve fmt --stamp` (in any Carve engine) records the spec version a document was
last processed under. carve-go reads that marker back, so a repository of stored
`.crv` files can be checked for documents predating a breaking spec change:

```go
stale, err := carve.NeedsReview(source)
```

An **unstamped** document reports `true`: its provenance is unknown, and assuming
it is current is the unsafe direction. The answer matches carve-php, carve-js and
carve-rs on the same document - the marker format is the contract, not any one
API - and the tests here read markers written by each of them.

What a version difference means for a stored document is the
[versioning contract](https://markup-carve.github.io/carve/versioning): only
`[behavior]` changelog entries between the stamped version and yours can require
a document change.

Carve inline conventions (note these differ from Markdown):

- `*x*` renders as `<strong>x</strong>` (bold)
- `/x/` renders as `<em>x</em>` (italic)

## Resource limits and untrusted input

The embedded engine runs in the wazero wasm runtime, which is hardened so a
single call cannot run away with host CPU or memory:

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

- **Memory cap.** Each instance's linear memory is capped at 512 MiB (8192
  wasm pages) via `WithMemoryLimitPages`, well under wazero's 4 GiB default
  ceiling. This is comfortably more than any reasonable Carve document needs,
  while preventing one input (or one per concurrent call) from exhausting host
  memory. An allocation past the cap fails gracefully inside the guest and is
  reported as a non-zero engine exit, rather than OOM-killing the host process.

### Content safety

Resource limits are only half of it. Carve's normative hardening is always on
and needs no option: dangerous URL schemes are blanked (`javascript:`, `data:`
and the rest of the spec denylist), event-handler attributes like `onclick` are
dropped, and the bidi override/isolate characters behind Trojan Source are
removed from rendered text.

Raw passthrough is the deliberate exception. A ` ```=html ` block or
`` `…`{=html} `` span is emitted **verbatim** by design, so it is the one thing
untrusted input must turn off:

```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()

html, err := carve.ToHTMLOptionsContext(ctx, untrusted, carve.Options{
    Safe:    true,      // escape =html blocks/spans instead of emitting them
    Profile: "comment", // full | article | comment | minimal
})
```

`Profile` restricts which constructs are allowed at all and caps input length.
The engine owns the list of valid names, so an unknown one comes back as an
error carrying the engine's message rather than being silently ignored.

Full recipe, defaults and threat model:
[Security](https://markup-carve.github.io/carve/security).

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
> (every diagram preset: mermaid, plantuml, d2, graphviz, wavedrom, abc,
> vega-lite, chart) and MathBlock, but **not** a Tabs / CodeGroup
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

The carve-rs revision the committed `.wasm` was built from is recorded in
[`internal/wasm/REV`](internal/wasm/REV). `build-wasm.sh` writes that file in
the same step that produces the bytes, and refuses to write it at all if the
carve-rs checkout is dirty - so the record cannot drift from the artifact the
way a hand-maintained comment does. (The crate is published as `carve-lang`,
but the CLI binary embedded here is `carve`.)

CI reads that file in the `engine-rev` job. It **fails** when the revision is
not a real commit on carve-rs `main` - a typo, a local-only build, a
force-pushed branch - and it reports how many commits behind `main` the engine
is as a **warning**, since carve-rs merging something is not a defect in this
repository.

Because the artifact is prebuilt, it can fall behind the spec with no change in
this repository - and it did, until the corpus job below started catching it. CI
runs the mandatory spec corpus through the **committed** `.wasm` and requires
byte-identical HTML, so a forgotten rebuild fails a build instead of quietly
shipping different output. `REV` says how stale the engine is; the corpus says
whether that staleness has started to matter.

The same job drives the corpus through `ParseAST` as well, so a node type or a
schema field name an engine rebuild drops is caught even where the rendered HTML
is unchanged. Locally, and **without a `-run` filter** - the two AST checks are
gated by the same variable, so filtering by name is how they came to run
nowhere:

```bash
CARVE_SPEC_CORPUS=/path/to/carve/tests/corpus go test ./...
```

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

`CARVE_RS` defaults to a sibling checkout that only exists on one developer's
machine, so anywhere else point it at a clone. For a published build, check out
the revision you want to ship first:

```bash
git clone https://github.com/markup-carve/carve-rs /tmp/carve-rs
git -C /tmp/carve-rs checkout <revision>
CARVE_RS=/tmp/carve-rs ./build-wasm.sh
```

`build-wasm.sh` writes that revision to `internal/wasm/REV`, so the artifact
identifies itself and release notes do not have to carry the sha by hand.

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
