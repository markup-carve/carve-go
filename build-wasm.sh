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

# Build the carve CLI for WASI.
( cd "${CARVE_RS}" && cargo build --release --target wasm32-wasip1 --bin carve )

# Ask cargo where it actually put the artifact instead of assuming
# $CARVE_RS/target. A shared CARGO_TARGET_DIR (a global config, a workspace, or
# the env var) moves it elsewhere, and the old hard-coded path then failed the
# copy AFTER a successful two-minute build - which reads like a build failure and
# leaves the previous .wasm silently in place.
TARGET_DIR="$(cd "${CARVE_RS}" && cargo metadata --format-version 1 --no-deps \
  | sed -n 's/.*"target_directory":"\([^"]*\)".*/\1/p')"
WASM="${TARGET_DIR:-${CARVE_RS}/target}/wasm32-wasip1/release/carve.wasm"

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

mkdir -p "${HERE}/internal/wasm"
cp "${WASM}" "${OUT}"
echo "${REV}" > "${HERE}/internal/wasm/REV"

echo "wrote ${OUT} from carve-rs ${REV}"
ls -la "${OUT}"
