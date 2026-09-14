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

// Non-HTML output targets. Every one of these is the same embedded engine with
// a different output flag, so they need no extra dependency and no rebuild.
func ToMarkdown(source string) (string, error)
func ToMarkdownContext(ctx context.Context, source string) (string, error)
func ToPlainText(source string) (string, error)
func ToPlainTextContext(ctx context.Context, source string) (string, error)
func ToANSI(source string) (string, error)
func ToANSIContext(ctx context.Context, source string) (string, error)

// ToCarve renders back to CANONICAL Carve source - the same transformation
// `carve fmt` performs, returned as a string. It is idempotent.
func ToCarve(source string) (string, error)
func ToCarveContext(ctx context.Context, source string) (string, error)

// Import foreign markup into canonical Carve with a machine-readable report.
func FromHTML(source string) (MigrationResult, error)
func FromMarkdown(source string) (MigrationResult, error)

// Reports use schema version 2. Fidelity is preserved, normalized, degraded,
// or dropped; confidence is exact, inferred, or fallback. Markdown currently
// reports dropped/fallback conservatively because its engine path does not
// expose construct-level fidelity.

// Render is the general form, for a non-HTML format WITH options.
func Render(source string, format OutputFormat, opts Options) (string, error)
func RenderContext(ctx context.Context, source string, format OutputFormat, opts Options) (string, error)

// OutputHTML is the zero value, so a caller that never names a format keeps
// getting HTML.
type OutputFormat string

const (
	OutputHTML      OutputFormat = ""            // HTML (CLI --html)
	OutputMarkdown  OutputFormat = "--markdown"  // Markdown
	OutputPlainText OutputFormat = "--plain"     // unstyled plain text
	OutputANSI      OutputFormat = "--ansi"      // plain text with ANSI styling
	OutputCarve     OutputFormat = "--carve"     // canonical Carve source
)

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

Two boundaries on the non-HTML targets, both measured rather than assumed:

- **`Options.Static` is HTML-only** and is REJECTED with an error for any other
  format rather than ignored. A caller who asked for static output and silently
  got interactive output back would have no way to notice.
- **`Options.Symbols` reaches HTML only.** The engine's Markdown, plain-text and
  ANSI renderers each emit a `:name:` shortcode literally and never consult the
  map. That is defensible for Markdown, where the consumer may have its own
  shortcode support, and correct for `OutputCarve`, where canonical source keeps
  what the author wrote - but it means a terminal render shows `:tick:` rather
  than the glyph you mapped. A test pins the behavior so a future engine that
  changes it cannot do so silently.

Not available here: **`lint`**. It exists in carve-rs as a library API
(`carve::lint_carve`) and has no CLI surface, and carve-go reaches the engine
only across the WASI stdio/argv boundary. It arrives once the engine grows a
`carve lint` subcommand and the embedded artifact is rebuilt past it.

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

A document over the profile's length cap is **refused**, not truncated and not
quietly dropped: the call returns an error carrying the engine's
`max_length_exceeded` line, naming the limit and the size given. `comment` caps
at 100,000 bytes and `minimal` at 10,000. The engine owns those numbers too, so
a caller has no reason to count bytes itself.

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

## Embedded engine

The package ships a committed WebAssembly build of carve-rs, pinned to a
recorded engine revision. Rebuild, conformance, publishing, and contributor
testing details are in the [development guide](docs/development.md).
