#!/usr/bin/env bash
#
# Refuse a tag whose name disagrees with what CHANGELOG.md claims.
#
# This is the leg no other check in this repository can reach. `go install` and
# `go get` resolve straight from the tag, and proxy.golang.org caches a module
# version immutably as soon as anything fetches it: there is no yank, no
# re-upload and no deprecation that takes the bytes back. The tag is the
# publish.
#
# There is also no version constant to compare the tag against - go.mod carries
# none and carve.go has none - so CHANGELOG.md is the only place this repository
# states which version it is.
#
# Usage: tools/version-gate.sh vX.Y.Z
set -uo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
CHANGELOG="$HERE/CHANGELOG.md"

tag="${1-}"
if [ -z "$tag" ]; then
  echo "version-gate: usage: tools/version-gate.sh vX.Y.Z" >&2
  exit 2
fi
if [ ! -r "$CHANGELOG" ]; then
  echo "version-gate: $CHANGELOG is not readable, so there is nothing to hold the tag to" >&2
  exit 2
fi

bad=0
note() { echo "version-gate: $1" >&2; bad=1; }

# A Go module version is the tag name verbatim. Without the leading v the proxy
# does not serve the tag as a version at all, so the shape is not cosmetic.
want=""
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*)
    if [[ "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
      want="${BASH_REMATCH[1]}"
    fi
    ;;
esac
if [ -z "$want" ]; then
  note "tag '$tag' is not vX.Y.Z; a Go module version is the tag name verbatim, so the leading v is required"
fi

heading="$(grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' "$CHANGELOG")"
released="$(printf '%s\n' "$heading" | sed -nE 's/^## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/p')"
unreleased="$(grep -m1 -E '^\[Unreleased\]:' "$CHANGELOG")"

echo "tag:                     $tag"
echo "newest released heading: ${heading:-(none)}"
echo "[Unreleased] link:       ${unreleased:-(none)}"

if [ -z "$released" ]; then
  note "CHANGELOG.md has no '## [X.Y.Z]' heading, so it claims no released version"
elif [ -n "$want" ] && [ "$released" != "$want" ]; then
  note "tag says $want, the newest released CHANGELOG heading says $released"
fi

# The date is what distinguishes a described release from a section somebody
# opened and left to fill in.
if [ -n "$released" ] &&
   ! printf '%s\n' "$heading" | grep -qE "^## \[$released\] - [0-9]{4}-[0-9]{2}-[0-9]{2}\$"; then
  note "the '[$released]' heading carries no '- YYYY-MM-DD' date: $heading"
fi

# Without the link definition the heading renders as literal brackets on
# GitHub and nothing points at the diff the release contains.
if [ -n "$want" ] && ! grep -qE "^\[$want\]: " "$CHANGELOG"; then
  note "CHANGELOG.md defines no '[$want]:' link at the foot"
fi

if [ -z "$unreleased" ]; then
  note "CHANGELOG.md defines no '[Unreleased]:' link at the foot"
elif [ -n "$want" ] &&
     ! printf '%s\n' "$unreleased" | grep -qF "/compare/v$want...HEAD"; then
  note "[Unreleased] does not compare from v$want...HEAD: $unreleased"
fi

if [ "$bad" -ne 0 ]; then
  echo "version-gate: REFUSED" >&2
  exit 1
fi
echo "version-gate: $tag agrees with CHANGELOG.md"
