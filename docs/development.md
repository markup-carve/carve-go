# Development

## How the embedded `.wasm` is built

The embedded artifact at `internal/wasm/carve.wasm` is the carve-rs CLI
compiled to `wasm32-wasip1`. That CLI already implements the exact contract this
module needs:

- reads Carve source from **stdin** when no file argument is given,
- writes rendered **HTML to stdout** (the default `--html` format),
- appends a single trailing newline if the output lacks one,
- accepts `--static` and `--extensions` for the
  [static render mode](reference.md#static-render-mode).

The carve-rs revision the committed `.wasm` was built from is recorded in
[`internal/wasm/REV`](../internal/wasm/REV), and what those bytes hash to in
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
