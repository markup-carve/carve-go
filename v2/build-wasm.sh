#!/usr/bin/env bash
# Build the 0.2 WASI JSON boundary from a clean carve-rs main checkout.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rs="${CARVE_RS:-$(dirname "$here")/carve-rs}"
if [ ! -f "$rs/Cargo.toml" ] || ! grep -q '^name = "carve-lang"' "$rs/Cargo.toml"; then
  echo 'carve: set CARVE_RS to a carve-rs main checkout' >&2
  exit 1
fi
if [ -n "$(git -C "$rs" status --porcelain)" ]; then
  echo 'carve: carve-rs checkout must be clean' >&2
  exit 1
fi
rev="$(git -C "$rs" rev-parse HEAD)"
if ! git -C "$rs" merge-base --is-ancestor "$rev" origin/main; then
  echo 'carve: engine commit must be on origin/main' >&2
  exit 1
fi
build_dir="${CARGO_TARGET_DIR:-/var/tmp/cargo-shared/carve-rs}"
remap="--remap-path-prefix=$rs=/carve-rs --remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=/cargo"
(
  cd "$rs"
  CARGO_BUILD_JOBS=4 CARGO_TARGET_DIR="$build_dir" RUSTFLAGS="${RUSTFLAGS:-} $remap" \
    cargo build --release --target wasm32-wasip1 --bin carve
)
artifact="$build_dir/wasm32-wasip1/release/carve.wasm"
if [ ! -s "$artifact" ]; then
  echo 'carve: WASI build produced no artifact' >&2
  exit 1
fi
cp "$artifact" "$here/internal/wasm/carve.wasm"
printf '%s\n' "$rev" > "$here/internal/wasm/REV"
(cd "$here/internal/wasm" && sha256sum carve.wasm > carve.wasm.sha256)
echo "Pinned carve-rs main $rev"
