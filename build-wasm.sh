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
# Pinned carve-rs revision used to generate the committed .wasm:
#   branch main, commit e83f889e044474d232562eb4e5173ea5839614b7
#   (loose definition bodies + `+` continuation + lazy continuation)
#
# Usage:
#   CARVE_RS=/path/to/carve-rs ./build-wasm.sh
#
# CARVE_RS defaults to the sibling checkout used during development. For a
# reproducible build, clone the pinned revision and point CARVE_RS at it:
#   git clone https://github.com/markup-carve/carve-rs /tmp/carve-rs-static
#   git -C /tmp/carve-rs-static checkout ce9c84e904e99548089aad8f4f73043a4c98e788
#   CARVE_RS=/tmp/carve-rs-static ./build-wasm.sh
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

mkdir -p "${HERE}/internal/wasm"
cp "${CARVE_RS}/target/wasm32-wasip1/release/carve.wasm" "${OUT}"

echo "wrote ${OUT}"
ls -la "${OUT}"
