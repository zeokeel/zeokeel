#!/bin/sh
# Local release build: the same artifacts the release CI workflow publishes.
#
# Usage:
#   sh scripts/release.sh                 # version from git describe
#   sh scripts/release.sh --dry-run       # validate prerequisites/plan only
#   ZEOKEEL_VERSION=1.2.3 sh scripts/release.sh
#
# Produces in dist/:
#   zeokeel-linux-{amd64,arm64}.tar.gz
#   zeokeel-windows-amd64.zip (+ GUI/console exe)
#   zeokeel-macos-{arm64,amd64}.zip (unsigned .app)
#   SHA256SUMS.txt
#
# Then publish with a tag: git tag v<version> && git push origin v<version>
# (.github/workflows/release.yml builds and attaches the same files).
#
# Signing/notarization is NOT part of this script (needs certificates; AR-03).
set -eu
cd "$(dirname "$0")/.."

export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

VERSION=${ZEOKEEL_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)}
echo "release: version=$VERSION"

if [ "${1:-}" = "--dry-run" ]; then
	echo "dry-run: no build executed"
	echo "prerequisites:"
	missing=0
	for tool in go git tar zip; do
		if command -v "$tool" >/dev/null 2>&1; then
			echo "  $tool: $(command -v "$tool")"
		else
			echo "  $tool: MISSING"; missing=1
		fi
	done
	if command -v sha256sum >/dev/null 2>&1; then
		echo "  sha256sum: $(command -v sha256sum)"
	elif command -v shasum >/dev/null 2>&1; then
		echo "  shasum: $(command -v shasum)"
	else
		echo "  sha256sum/shasum: MISSING"; missing=1
	fi
	echo "planned artifacts under dist/:"
	echo "  zeokeel-linux-{amd64,arm64}.tar.gz"
	echo "  zeokeel-macos-{arm64,amd64}.zip (unsigned .app)"
	echo "  zeokeel-windows-amd64.zip (+ GUI/console exe)"
	echo "  SHA256SUMS.txt"
	echo "would run: scripts/build.sh package-linux package-macos package-windows"
	case "$VERSION" in
	[0-9]*) echo "publish: git tag v$VERSION && git push origin v$VERSION  (CI release.yml)" ;;
	*) echo "publish: git tag v<X.Y.Z> && git push origin v<X.Y.Z>  (CI release.yml; set ZEOKEEL_VERSION for a release build)" ;;
	esac
	[ "$missing" = 0 ] || { echo "release: missing prerequisite(s)" >&2; exit 1; }
	echo "release dry-run: OK"
	exit 0
fi

echo "== packages =="
sh scripts/build.sh package-linux
sh scripts/build.sh package-macos
sh scripts/build.sh package-windows

echo "== checksums =="
(
	cd dist
	set --
	for f in zeokeel-*.tar.gz zeokeel-*.zip zeokeel-*.exe; do
		[ -f "$f" ] && set -- "$@" "$f"
	done
	if [ "$#" -gt 0 ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			sha256sum "$@" >SHA256SUMS.txt
		else
			shasum -a 256 "$@" >SHA256SUMS.txt
		fi
		cat SHA256SUMS.txt
	fi
)

echo
echo "release artifacts under dist/:"
ls -l dist/zeokeel-*.tar.gz dist/zeokeel-*.zip dist/zeokeel-*.exe 2>/dev/null || true
echo
echo "publish:"
echo "  git tag v$VERSION && git push origin v$VERSION   # CI release.yml publishes"
