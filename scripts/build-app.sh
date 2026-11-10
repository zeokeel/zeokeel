#!/usr/bin/env bash
# Build the framework-native ZeoKeel desktop packages with `go tool mygo build`.
#
# This is the one build entry point used both locally (`make package`) and by
# CI. It:
#
#   1. generates every raster icon from logo/logo.svg + logo/logo-dark.svg
#      (scripts/gen-icons.sh: the single logo source, assets/ is gitignored);
#   2. syncs `version` in mygo.json from VERSION, the one version source, so a
#      release bump is one file edit;
#   3. exports MYGO_LDFLAGS with the build identity of internal/host
#      (Version/Commit/BuildTime), as a plain `go build` release would;
#   4. execs `go tool mygo build "$@"`, passing every extra mygo flag through.
#
# Usage:
#   scripts/build-app.sh -platform linux/amd64,windows/amd64
#   scripts/build-app.sh -platform darwin/arm64 -skip-notarize
#
# Packaging comes from mygo.json: Windows GUI executable + NSIS installer,
# Linux executable/desktop/icon/deb/tar.gz/PKGBUILD, macOS .app + .dmg with
# signing and notarization (macOS runners only). There is deliberately no
# `updates` block: keyless updates are the app module's, not mygo's.
#
# Requirements (native UI needs no frontend and no JavaScript runtime):
#   go (with the `tool github.com/egoist/mygo/cmd/mygo` directive in go.mod),
#   jq, git (optional: Commit/BuildTime fall back to "unknown" outside a
#   git checkout), and rsvg-convert (see scripts/gen-icons.sh).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

die() {
	printf 'build-app: %s\n' "$*" >&2
	exit 1
}

[ -f mygo.json ] || die "mygo.json not found in $ROOT"
[ -f VERSION ] || die "VERSION not found in $ROOT"
[ -f scripts/gen-icons.sh ] || die "scripts/gen-icons.sh not found in $ROOT"

if ! command -v go >/dev/null 2>&1; then
	die "go not found in PATH (this project needs the Go toolchain of go.mod)"
fi
if ! command -v jq >/dev/null 2>&1; then
	die "jq not found — install jq (it keeps mygo.json in sync with VERSION)"
fi

# 1. Icons: the only logo sources are logo/*.svg; every PNG here is derived.
bash "$ROOT/scripts/gen-icons.sh"

# 2. One version source: VERSION -> mygo.json.
version="$(tr -d '[:space:]' <VERSION)"
[ -n "$version" ] || die "VERSION is empty"
case "$version" in
*\"* | *\'* | *[[:space:]]*)
	die "VERSION ($version) holds a quote or a space, which the -ldflags value cannot"
	;;
esac
tmp="$(mktemp "${TMPDIR:-/tmp}/zeokeel-mygo-json.XXXXXX")"
if ! jq --arg v "$version" '.version = $v' mygo.json >"$tmp"; then
	rm -f "$tmp"
	die "jq could not rewrite mygo.json"
fi
if cmp -s "$tmp" mygo.json; then
	rm -f "$tmp"
else
	mv -f "$tmp" mygo.json
	echo "build-app: mygo.json version -> $version"
fi

# 3. Build identity of internal/host, added after mygo's own -ldflags.
if commit="$(git rev-parse --short=12 HEAD 2>/dev/null)"; then
	build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
else
	commit="unknown"
	build_time="unknown"
fi
export MYGO_LDFLAGS="-X github.com/zeokeel/zeokeel/internal/host.Version=v$version -X github.com/zeokeel/zeokeel/internal/host.Commit=$commit -X github.com/zeokeel/zeokeel/internal/host.BuildTime=$build_time"
echo "build-app: version=v$version commit=$commit buildTime=$build_time"

# 4. mygo builds and packages into <out>/<goos>-<goarch> (out is "build").
exec go tool mygo build "$@"
