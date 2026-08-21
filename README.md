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
	Static     bool              // self-contained static HTML (CLI --static; implies --extensions)
	Extensions []string          // enable bundled interactive extensions (CLI --extensions)
	Safe       bool              // escape =html raw blocks/spans (CLI --safe)
	Profile    string            // full|article|comment|minimal (CLI --profile)
	Symbols    map[string]string // render :name: shortcodes (CLI --symbol NAME=VALUE)
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

### Symbol shortcodes

`Symbols` maps a shortcode name to the text that replaces it, so `:name:` in the
source renders as that value:

```go
html, err := carve.ToHTMLOptions("Ship it :rocket:", carve.Options{
    Symbols: map[string]string{"rocket": "\U0001F680"},
})
// <p>Ship it 🚀</p>
```

A name the map does not carry is left alone - `:unknown:` stays literal text
rather than becoming an error or an empty string - and the engine's
word-boundary rule is unchanged by the map, so a glued run like `a:rocket:b`,
`3:rocket:4` or a `` `:rocket:` `` code span still does not substitute. That is
what makes a map safe to enable for a whole site: it cannot rewrite times,
ratios or package paths that happen to contain colons.

> [!WARNING]
> Values are substituted **raw**, exactly as written, and are **not** escaped.
> That is deliberate across every Carve engine - it is what lets a symbol expand
> to markup such as an `<img>` tag - but it means the map is *trusted processor
> configuration*, on the same footing as the code calling this package. **NEVER
> build a symbols map out of untrusted or user-supplied input.** A value is a
> script-injection vector, and `Safe` does not constrain it: `Safe` governs
> `=html` in the **document**, not this configuration. Populate it from your own
> site or application config and nowhere else.

Keys are sorted before they are handed to the engine, so the same map always
produces the same invocation. (Go randomizes map iteration on purpose; passing
that order straight through would make each call build a different command line
and any test asserting on it flake.)

An entry that could not reach the engine intact is refused with an error rather
than silently reshaped - a name may not be empty or contain `=`, and neither
half may contain a NUL. The `=` rule is the load-bearing one: the engine splits
each argument at its **first** `=`, so a name of `a=b` with a value of `c` would
otherwise register `a` mapped to `b=c`, a different map than you wrote, with
nothing reporting it. A name the engine's shortcode grammar cannot match is not
rejected, only inert.

There is no practical ceiling on the map's size here. `--symbol` is repeatable
rather than file-based, so a large map means a large argument list, which on an
engine driven as a **subprocess** would eventually meet `ARG_MAX`. carve-go
spawns no process: the engine is embedded wasm and the arguments go into guest
linear memory through wazero, so the governing limit is `maxMemoryPages`, not
`ARG_MAX`. A full emoji set (~3800 entries, ~92 KiB of arguments) renders in
tens of milliseconds; 100000 entries (~2.5 MiB) still renders.

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
[`internal/wasm/REV`](internal/wasm/REV), and what those bytes hash to in
`internal/wasm/carve.wasm.sha256`. `build-wasm.sh` writes both in the same step
that produces the artifact, and refuses to write anything at all if the carve-rs
checkout is dirty - so the record cannot drift from the artifact the way a
hand-maintained comment does. (The crate is published as `carve-lang`, but the
CLI binary embedded here is `carve`.)

CI reads all three in the `engine-rev` job, through the shared reader carve-rs
ships at `tools/check-engine-pin.py`. The rule lives there rather than here, so
every binding that pins this engine inherits it instead of spelling it out
again. The job **fails** when the revision is missing, is not 40 lowercase hex,
is not a real commit, is not an ancestor of `main`, or when the committed
`.wasm` does not hash to the recorded digest.

The lag behind `main` is **printed as a number and never gates**. It briefly
gated on age, at fourteen days, and that was deleted rather than retuned: age is
a proxy for "has the engine changed in a way that matters", and a poor one -
carve-rs can merge ten commits that touch no rendering, or one that lands a
container ruling and moves fifty documents. Commit distance is the same proxy in
a different unit, red from the moment any PR opens upstream and unclearable by
the action it recommends. The number that actually answers the question is
measured directly by the `corpus-drift` job below.

Both of those replaced a `::warning::` annotation, which could not fail a job at
all - and this repository is the evidence that a warning is not enough, having
carried one throughout while being the binding furthest behind, with a green
scheduled run.

What the digest buys, precisely: a `carve.wasm` swapped in, truncated, or
committed from any build other than the one that wrote the digest fails CI. What
it does not buy: a `REV` hand-edited on its own still passes, because nothing
ties the revision to the digest cryptographically. Closing that would need CI to
rebuild from `REV` and compare, which needs the full Rust and WASI toolchain in
the job, and the build is not byte-reproducible across checkout paths anyway.
The three files being written together is the guarantee.

Because the artifact is prebuilt, it can render the spec's documents wrongly with
no change in this repository at all. Two CI jobs measure that, and they ask
different questions:

- **`corpus` gates.** It runs the mandatory spec corpus through the
  **committed** `.wasm` and requires byte-identical HTML, against **the spec
  commit the embedded engine pins** - `REV` names a carve-rs commit, and that
  commit's `tests/spec` gitlink names the spec it was written against. So the
  question is whether the committed bytes are as conformant as the engine they
  were built from, which is a question this repository can answer and act on. A
  stale, swapped or half-committed rebuild fails it.
- **`corpus-drift` reports.** It runs the same comparison against spec `main`
  and prints one line naming the number, to the job log, the step summary and a
  notice annotation. It never fails on that number - no change here can make an
  engine implement a ruling it has not implemented yet - but it does fail when
  it did not measure one, so it cannot quietly become decoration.

This split replaced a single job that gated against spec `main`. That version
was red whenever the spec was ahead of the engine, which is most of the time and
is not something a pull request here can fix; a gate in that state teaches every
reader to skip it. The direct measurement is still taken and still printed, it
just no longer blocks work it has nothing to do with.

Both jobs drive the corpus through `ParseAST` as well, so a node type or a
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

`build-wasm.sh` writes that revision to `internal/wasm/REV` and the artifact's
digest to `internal/wasm/carve.wasm.sha256`, so the artifact identifies itself
and release notes do not have to carry the sha by hand. Commit all three
together; CI checks the digest against the committed bytes.

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
