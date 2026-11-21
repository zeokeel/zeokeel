#!/usr/bin/env bash
# publish-rig-local.sh VERSION [RIG_DIR] [PROXY_DIR]
# publish-rig-local.sh SUBMODULE VERSION [RIG_DIR] [PROXY_DIR]
#
# Offline development helper: build a Go module proxy entry for a tag (or any
# commit) of the rig working tree, so this product can consume an unreleased SDK
# version without network access. For the root module nested modules are
# stripped (a module zip may only contain the root module); for a nested module
# the tag is "<submodule>/<version>" and only that subtree ships.
#
#   scripts/publish-rig-local.sh v0.27.0-rc.5
#   scripts/publish-rig-local.sh session/sqlstore v0.1.0
#   GOPROXY=file://$PWD/.cache/rigproxy GOSUMDB=off go get github.com/zeokeel/zeokeel-rig@v0.27.0-rc.5
#
# The generated files live under the proxy dir; the module cache keeps the
# extracted copy, so later builds with GOPROXY=off stay reproducible.
set -euo pipefail
SUB=""
if [ $# -ge 2 ] && [ -f "${RIG_DIR:-/data/zeokeel-rig}/$1/go.mod" ]; then
  SUB=$1
  shift
fi
VER=${1:?usage: publish-rig-local.sh [SUBMODULE] VERSION [RIG_DIR] [PROXY_DIR]}
RIG=${2:-${RIG_DIR:-/data/zeokeel-rig}}
PROXY=${3:-${RIG_PROXY_DIR:-$(cd "$(dirname "$0")/.." && pwd)/.cache/rigproxy}}
TAG=$VER
MODULE=github.com/zeokeel/zeokeel-rig
SRC=$RIG
if [ -n "$SUB" ]; then
  TAG="$SUB/$VER"
  MODULE="github.com/zeokeel/zeokeel-rig/$SUB"
  SRC="$RIG/$SUB"
fi
mkdir -p /tmp/zeokeel-tmp
export TMPDIR=${TMPDIR:-/tmp/zeokeel-tmp}
OUT="$PROXY/$MODULE/@v"
mkdir -p "$OUT"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/tree"
if [ -n "$SUB" ]; then
  # Only the submodule subtree ships, at the zip root; Go also carries the
  # repository-root LICENSE into a submodule zip, so it must be present for the
  # zip hash to match the canonical module proxy.
  depth=$(awk -F/ '{print NF}' <<<"$SUB")
  git -C "$RIG" archive --format=tar "$TAG" -- "$SUB" | tar -x -C "$work/tree" --strip-components="$depth"
  if [ -f "$RIG/LICENSE" ]; then
    cp "$RIG/LICENSE" "$work/tree/LICENSE"
  fi
else
  git -C "$RIG" archive --format=tar "$TAG" | tar -x -C "$work/tree"
  while IFS= read -r mod; do
    rm -rf "$(dirname "$mod")"
  done < <(find "$work/tree" -mindepth 2 -name go.mod)
fi
mkdir -p "$work/zip/$MODULE@$VER"
cp -a "$work/tree/." "$work/zip/$MODULE@$VER/"
rm -f "$OUT/$VER.zip"
(cd "$work/zip" && zip -qrD "$OUT/$VER.zip" "$MODULE@$VER")
cp "$SRC/go.mod" "$OUT/$VER.mod"
printf '{"Version":"%s","Time":"%s"}\n' "$VER" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$OUT/$VER.info"
echo "published $TAG -> $OUT"
