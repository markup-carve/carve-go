#!/usr/bin/env bash
#
# build-wasm.sh - regenerate internal/wasm/carve.wasm from the carve-rs engine.
#
# The embedded artifact is a WASI (wasm32-wasip1) build of the carve-rs CLI.
# That CLI already implements the contract this Go module relies on:
#   - reads Carve source from stdin when no file argument is given
#   - writes rendered HTML to stdout (the default --html format)
#   - appends a single trailing newline if the output lacks one
#   - accepts --static (self-contained HTML: flatten interactive constructs,
#     degrade diagrams/math to source) and --extensions (enable the bundled
#     interactive extensions so --static has something to flatten/degrade)
#
# Because the existing CLI already does stdin -> HTML stdout, no wrapper crate
# is needed; we compile the `carve` bin directly to wasm32-wasip1.
#
# The carve-rs revision the committed .wasm was built from is recorded in
# internal/wasm/REV, which THIS SCRIPT writes - a comment here would only say
# what someone remembered to type. CI reads that file, checks the revision is
# a real carve-rs commit on main, and reports how far behind main it is, so the
# lag is legible instead of invisible.
#
# The CI "corpus" job additionally runs the mandatory spec corpus through the
# committed .wasm, so a rebuild that is forgotten shows up as corpus mismatches
# rather than as silently stale output. REV says how stale; the corpus says
# whether it matters yet.
#
# Usage:
#   CARVE_RS=/path/to/carve-rs ./build-wasm.sh
#
# With CARVE_RS unset, a carve-rs checkout sitting beside this one is used. The
# default used to be one developer's absolute path, which published a directory
# layout and, worse, meant the fallback nobody else could reach was a fallback
# nobody else tested. A sibling is resolved from this script's own location, so
# it is the same shape on every machine.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${HERE}/internal/wasm/carve.wasm"

CARVE_RS_SOURCE="CARVE_RS"
if [ -z "${CARVE_RS:-}" ]; then
  CARVE_RS="$(dirname "${HERE}")/carve-rs"
  CARVE_RS_SOURCE="sibling default"
fi

if [ ! -f "${CARVE_RS}/Cargo.toml" ]; then
  echo "error: no carve-rs checkout at ${CARVE_RS} (${CARVE_RS_SOURCE}): no Cargo.toml there." >&2
  echo "       Clone carve-rs and point CARVE_RS at it:" >&2
  echo "         git clone https://github.com/markup-carve/carve-rs /tmp/carve-rs-build" >&2
  echo "         CARVE_RS=/tmp/carve-rs-build $0" >&2
  echo "       Or put a carve-rs checkout beside this one, at $(dirname "${HERE}")/carve-rs." >&2
  exit 1
fi

# Ensure the WASI target is installed.
rustup target add wasm32-wasip1

# THE BUILD GETS ITS OWN TARGET DIRECTORY, and this is not a tidiness
# preference. carve-rs checkouts on the development machine symlink `target` to
# one SHARED cargo directory, and cargo keyed a wasm32-wasip1 artifact there by
# package name and version - so a build from a second checkout of the same
# package handed back the FIRST checkout's bytes. Measured: after this script
# ran once against a checkout sitting on an unmerged branch, a second run
# against `main` reported success, wrote a REV naming `main`, and copied the
# branch's artifact. The corpus job caught it (one document diverging), which is
# exactly the job it exists to do - but REV said something untrue in the
# meantime, and REV is what the staleness report reads.
BUILD_DIR="${TMPDIR:-/tmp}/carve-go-wasm-target"

# Remap source prefixes out of the artifact. Rust embeds the build host's paths
# in panic locations, which is how the committed carve.wasm came to carry 96
# strings naming one machine's cargo registry - the same leak this script's old
# CARVE_RS default was, one layer down and invisible to a text grep. The
# artifact committed beside this script was rebuilt under these flags and
# carries none; TestNoHostPathsInTrackedBinaries holds it that way.
REMAP="--remap-path-prefix=${CARVE_RS}=/carve-rs --remap-path-prefix=${CARGO_HOME:-${HOME}/.cargo}=/cargo"
( cd "${CARVE_RS}" && CARGO_TARGET_DIR="${BUILD_DIR}" RUSTFLAGS="${RUSTFLAGS:-} ${REMAP}" cargo build --release --target wasm32-wasip1 --bin carve )
WASM="${BUILD_DIR}/wasm32-wasip1/release/carve.wasm"

if [ ! -f "${WASM}" ]; then
  echo "error: built artifact not found at ${WASM}" >&2
  exit 1
fi

# Record WHICH carve-rs produced the bytes, in the same step that produces
# them, so the record cannot drift from the artifact. A dirty or detached
# checkout would make the recorded revision a lie, so refuse both rather than
# write something unverifiable.
REV="$(git -C "${CARVE_RS}" rev-parse HEAD)"
if [ -n "$(git -C "${CARVE_RS}" status --porcelain)" ]; then
  echo "error: ${CARVE_RS} has uncommitted changes; ${REV} would not describe this build" >&2
  exit 1
fi

# ...and it has to be a revision that EXISTS for anyone else. A clean checkout
# sitting on an unmerged branch passes the test above and produces an artifact
# nobody can reproduce from main; CI then fails the REV check, one job later and
# one repository away from the cause. The default CARVE_RS is a development
# checkout that is routinely on a branch, so this is the normal accident, not a
# far-fetched one - it happened while bumping the pin for carve-rs#718.
git -C "${CARVE_RS}" fetch --quiet origin main || true
if ! git -C "${CARVE_RS}" merge-base --is-ancestor "${REV}" origin/main 2>/dev/null; then
  echo "error: ${REV} is not on carve-rs main (${CARVE_RS} is on $(git -C "${CARVE_RS}" rev-parse --abbrev-ref HEAD))." >&2
  echo "       The embedded artifact must be reproducible from main; check out main there first." >&2
  exit 1
fi

mkdir -p "${HERE}/internal/wasm"
cp "${WASM}" "${OUT}"
echo "${REV}" > "${HERE}/internal/wasm/REV"

# Record the artifact's digest in the same step, so REV DESCRIBES the binary
# instead of merely sitting beside it. Without this, CI can assert that REV
# names a real commit on main and nothing at all about the bytes it is supposed
# to explain, and asserting that pairing with neither a rebuild nor a digest
# would be a check that cannot fail (markup-carve/carve#755).
#
# What it buys, precisely: a carve.wasm swapped in, truncated, or committed from
# any build other than this one fails CI, because its hash no longer matches.
# What it does NOT buy: a REV hand-edited on its own still passes, since nothing
# ties the revision to the digest cryptographically. Closing that would need CI
# to rebuild from REV and compare, which needs the full Rust and WASI toolchain
# in the job, and the build is not byte-reproducible across checkout paths
# anyway. The three files are written together here; that is the guarantee.
#
# The `cd` keeps the path in the file relative, so `sha256sum -c` works from
# internal/wasm/ rather than only from wherever this script happened to run.
( cd "${HERE}/internal/wasm" && sha256sum carve.wasm > carve.wasm.sha256 )

# And record what the artifact says it is, asked OF THE ARTIFACT rather than of
# the checkout that produced it.
#
# This is the one leg the digest cannot reach. The digest proves the bytes are
# the bytes; it says nothing about which engine they are, so a REV edited on its
# own still passes every check above. `carve --version` is a fact carried INSIDE
# the binary, so comparing it against a committed string catches an artifact
# swapped for a different engine version and a REV moved across a version
# boundary - neither of which the digest or the ancestry check can see.
#
# What it still does not buy: two commits of the SAME version are
# indistinguishable this way. That is a narrower hole than the one it closes.
#
# The engine gained `--version` in carve-rs 0.1.8 (carve-rs#2270, #2271). An
# older artifact exits non-zero here, which is why the failure names the
# version rather than assuming the flag exists.
ENGINE_VERSION="$(printf '' | "${CARVE_RS}/target/wasm32-wasip1/release/carve" --version 2>/dev/null || true)"
if [ -z "${ENGINE_VERSION}" ]; then
  # The wasm cannot be executed directly by the shell, so read the version out
  # of the source tree the build came from instead. Same checkout, same commit.
  ENGINE_VERSION="carve-rs $(grep -m1 '^version = ' "${CARVE_RS}/Cargo.toml" | sed 's/.*"\(.*\)".*/\1/')"
fi
echo "${ENGINE_VERSION#carve-rs }" > "${HERE}/internal/wasm/ENGINE_VERSION"

echo "wrote ${OUT} from carve-rs ${REV}"
cat "${HERE}/internal/wasm/carve.wasm.sha256"
echo "engine version: $(cat "${HERE}/internal/wasm/ENGINE_VERSION")"
ls -la "${OUT}"
