#!/bin/sh
# Hard gate C1: pure Go, no cgo. See docs/PLAN.md §3.3.
set -eu
export CGO_ENABLED=0
: "${GO:=go}"

echo "== build"
$GO build ./...

echo "== cgo dependency check"
out=$($GO list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...)
if [ -n "$out" ]; then
	echo "cgo dependencies found:"
	echo "$out"
	exit 1
fi

echo "== cross build"
# Cross-built binaries go to a scratch directory: building without -o writes
# into the current working directory, which overwrote the repo-root zeokeel
# with the last target (darwin/arm64) and left the checkout with a binary that
# cannot run on the host.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
for t in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
	GOOS=${t%/*} GOARCH=${t#*/} $GO build -o "$tmp/zeokeel-${t%/*}-${t#*/}" ./cmd/zeokeel
	echo "  $t ok"
done

echo "PASS (zero cgo)"
