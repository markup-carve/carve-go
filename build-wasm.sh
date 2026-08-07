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
# CARVE_RS defaults to the sibling checkout used during development, which only
# exists on one machine. Anywhere else, clone carve-rs and point CARVE_RS at it:
#   git clone https://github.com/markup-carve/carve-rs /tmp/carve-rs-build
#   CARVE_RS=/tmp/carve-rs-build ./build-wasm.sh
set -euo pipefail

CARVE_RS="${CARVE_RS:-/media/mark/data/work/git/carve-rs}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${HERE}/internal/wasm/carve.wasm"

if [ ! -f "${CARVE_RS}/Cargo.toml" ]; then
  echo "error: carve-rs not found at ${CARVE_RS} (set CARVE_RS=...)" >&2
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
( cd "${CARVE_RS}" && CARGO_TARGET_DIR="${BUILD_DIR}" cargo build --release --target wasm32-wasip1 --bin carve )
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

echo "wrote ${OUT} from carve-rs ${REV}"
cat "${HERE}/internal/wasm/carve.wasm.sha256"
ls -la "${OUT}"
