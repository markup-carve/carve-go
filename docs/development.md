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

CI (`engine-pin`) checks that the artifact matches its digest and that `REV` is
a full commit on carve-rs `main`.

## Cutting a release

A Go module version is the tag name, and `proxy.golang.org` caches it
immutably once anything fetches it. Rehearse the `Tag gate` workflow against the
commit before the tag exists:

```bash
gh workflow run tag.yml --repo markup-carve/carve-go --ref main -f tag=v0.2.0
```

It refuses the tag unless the name agrees with `CHANGELOG.md`, the commit is an
ancestor of `main`, the engine pin checks pass and the suite is green. The
`Release` workflow runs the same gates, waits for the `release-approval`
environment and creates the tag last.

## Testing

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```
