#!/usr/bin/env bash
# publish-rig-local.sh VERSION [RIG_DIR] [PROXY_DIR]
#
# Offline development helper: build a Go module proxy entry for a tag (or any
# commit) of the rig working tree, so this product can consume an unreleased SDK
# version without network access. Nested modules are stripped (a module zip may
# only contain the root module).
#
#   scripts/publish-rig-local.sh v0.27.0-rc.5
#   GOPROXY=file://$PWD/.cache/rigproxy GOSUMDB=off go get github.com/zeokeel/zeokeel-rig@v0.27.0-rc.5
#
# The generated files live under the proxy dir; the module cache keeps the
# extracted copy, so later builds with GOPROXY=off stay reproducible.
set -euo pipefail
VER=${1:?usage: publish-rig-local.sh VERSION [RIG_DIR] [PROXY_DIR]}
RIG=${2:-${RIG_DIR:-/data/zeokeel-rig}}
PROXY=${3:-${RIG_PROXY_DIR:-$(cd "$(dirname "$0")/.." && pwd)/.cache/rigproxy}}
mkdir -p /tmp/zeokeel-tmp
export TMPDIR=${TMPDIR:-/tmp/zeokeel-tmp}
OUT="$PROXY/github.com/zeokeel/zeokeel-rig/@v"
mkdir -p "$OUT"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/tree"
git -C "$RIG" archive --format=tar "$VER" | tar -x -C "$work/tree"
while IFS= read -r mod; do
  rm -rf "$(dirname "$mod")"
done < <(find "$work/tree" -mindepth 2 -name go.mod)
mkdir -p "$work/zip/github.com/zeokeel/zeokeel-rig@$VER"
cp -a "$work/tree/." "$work/zip/github.com/zeokeel/zeokeel-rig@$VER/"
rm -f "$OUT/$VER.zip"
(cd "$work/zip" && zip -qrD "$OUT/$VER.zip" "github.com/zeokeel/zeokeel-rig@$VER")
cp "$RIG/go.mod" "$OUT/$VER.mod"
printf '{"Version":"%s","Time":"%s"}\n' "$VER" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$OUT/$VER.info"
echo "published $VER -> $OUT"
