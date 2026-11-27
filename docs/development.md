# Development

## The embedded engine

`internal/wasm/carve.wasm` is the carve-rs `carve` command compiled to
`wasm32-wasip1`. The Go package talks to it through the public 0.2 JSON call
boundary (`carve call`). The file is committed: it is the shipped artifact.

`internal/wasm/REV` names the carve-rs commit the bytes were built from, and
`internal/wasm/carve.wasm.sha256` records their digest. `build-wasm.sh` writes
all three in one step and refuses a dirty carve-rs checkout or a commit that is
not on carve-rs `main`:

```bash
git clone https://github.com/markup-carve/carve-rs /tmp/carve-rs
git -C /tmp/carve-rs checkout <revision>
CARVE_RS=/tmp/carve-rs ./build-wasm.sh
```

With `CARVE_RS` unset the script uses a carve-rs checkout beside this one
(`../carve-rs`). It passes `--remap-path-prefix` so the artifact carries no host
paths; `TestNoHostPathsInTrackedBinaries` fails the suite if one does. Commit the
artifact, `REV` and the digest together.

CI checks the pin in three jobs:

- `engine-rev` fails when the artifact does not match its digest or `REV` is not
  a full commit on carve-rs `main`.
- `corpus` renders the `spec/0.2/conformance` entries of the spec commit the
  engine was built from (`spec-data/SOURCE_COMMIT` at `REV` in carve-rs) and
  fails on any difference.
- `corpus-drift` runs the same comparison against spec `main` and reports how
  many entries diverge. It fails only when nothing was measured.

Locally:

```bash
CARVE_CONFORMANCE=/path/to/carve/spec/0.2/conformance go test -run 'TestConformance$' -v .
```

## Cutting a release

A Go module version is the tag name, and `proxy.golang.org` caches it
immutably once anything fetches it. Rehearse the `Tag gate` workflow against the
commit before the tag exists:

```bash
gh workflow run tag.yml --repo markup-carve/carve-go --ref main -f tag=v0.2.0
```

It refuses the tag unless the name agrees with `CHANGELOG.md`, the commit is an
ancestor of `main`, the engine pin checks pass and the suite is green with the
conformance corpus of the pinned spec. The
`Release` workflow runs the same gates, waits for the `release-approval`
environment and creates the tag last.

## Testing

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```
