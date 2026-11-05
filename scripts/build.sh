#!/bin/sh
# Canonical build entry point.
#
# Every binary is written to dist/ with the same stable name on every run:
#
#   dist/zeokeel-<goos>-<goarch>[.exe]            one-shot build
#   dist/zeokeel-<goos>-<goarch>-console.exe      Windows console twin only
#   dist/zeokeel-windows-amd64.zip                Windows package (optional)
#
# Nothing is written to the repository root or to package directories. The
# release package is always dist/zeokeel-windows-amd64.zip and its inner file
# names are stable too (zeokeel-windows-amd64.exe / ...-console.exe).
#
# Usage:
#   sh scripts/build.sh                 # host binary (native)
#   sh scripts/build.sh linux-amd64     # one target: linux-amd64|linux-arm64|darwin-arm64
#   sh scripts/build.sh windows         # windows/amd64 GUI + console twins
#   sh scripts/build.sh all             # linux/amd64, linux/arm64, darwin/arm64, windows/amd64
#   sh scripts/build.sh package-windows # windows build + dist/zeokeel-windows-amd64.zip
#   sh scripts/build.sh clean           # remove every build output (dist/, stray root binaries, *.test)
#   sh scripts/build.sh check           # compile only: go build ./... (writes nothing)
set -eu
cd "$(dirname "$0")/.."

export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

DIST=dist
WIN_README=packaging/windows/README-WINDOWS.txt
WIN_GUI="$DIST/zeokeel-windows-amd64.exe"
WIN_CONSOLE="$DIST/zeokeel-windows-amd64-console.exe"
WIN_ZIP="$DIST/zeokeel-windows-amd64.zip"
WIN_ICO=logo/zeokeel.ico
WIN_RC=cmd/zeokeel/resource_windows_amd64.rc
WIN_SYSO=cmd/zeokeel/resource_windows_amd64.syso

usage() {
	sed -n "2,21p" "$0" | sed 's/^# \{0,1\}//'
}

die() {
	echo "build: $*" >&2
	exit 1
}

# target_bin prints the stable output path for one GOOS/GOARCH pair.
target_bin() {
	case "$1" in
	windows) printf '%s/zeokeel-%s-%s.exe' "$DIST" "$1" "$2" ;;
	*) printf '%s/zeokeel-%s-%s' "$DIST" "$1" "$2" ;;
	esac
}

# build_one builds one GOOS/GOARCH pair into its stable dist/ path.
build_one() {
	os=$1
	arch=$2
	out=$(target_bin "$os" "$arch")
	mkdir -p "$DIST"
	echo "  $out"
	GOOS="$os" GOARCH="$arch" go build -trimpath -o "$out" ./cmd/zeokeel
}

build_native() {
	os=$(go env GOOS)
	arch=$(go env GOARCH)
	build_one "$os" "$arch"
}

# ensure_windows_resource keeps the committed icon resource in sync with the
# .ico/.rc sources. The .syso is committed so a machine without MinGW binutils
# can still build an icon-bearing exe; when windres is available and a source is
# newer, it is regenerated here.
ensure_windows_resource() {
	[ -f "$WIN_SYSO" ] || die "missing $WIN_SYSO; run sh scripts/gen-windows-resource.sh"
	if [ "$WIN_ICO" -nt "$WIN_SYSO" ] || [ "$WIN_RC" -nt "$WIN_SYSO" ]; then
		if command -v x86_64-w64-mingw32-windres >/dev/null 2>&1; then
			sh scripts/gen-windows-resource.sh
		else
			echo "build: warning: $WIN_SYSO is older than its sources but windres is missing" >&2
		fi
	fi
}

build_windows() {
	ensure_windows_resource
	mkdir -p "$DIST"
	echo "  $WIN_GUI (GUI)"
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui" -o "$WIN_GUI" ./cmd/zeokeel
	echo "  $WIN_CONSOLE (console)"
	GOOS=windows GOARCH=amd64 go build -trimpath -o "$WIN_CONSOLE" ./cmd/zeokeel
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$@"
	else
		die "no sha256sum/shasum on PATH"
	fi
}

package_windows() {
	command -v zip >/dev/null 2>&1 || die "package-windows needs 'zip' on PATH"
	[ -f "$WIN_README" ] || die "missing $WIN_README"
	build_windows
	cp "$WIN_README" "$DIST/README-WINDOWS.txt"
	(
		cd "$DIST"
		sha256 zeokeel-windows-amd64.exe zeokeel-windows-amd64-console.exe >SHA256SUMS.txt
		rm -f zeokeel-windows-amd64.zip
		zip -q -X zeokeel-windows-amd64.zip \
			zeokeel-windows-amd64.exe zeokeel-windows-amd64-console.exe \
			README-WINDOWS.txt SHA256SUMS.txt
	)
	(
		cd "$DIST"
		sha256 zeokeel-windows-amd64.exe zeokeel-windows-amd64-console.exe \
			zeokeel-windows-amd64.zip >SHA256SUMS.txt
	)
	echo "  $WIN_ZIP"
}

# clean removes only build outputs. It deliberately leaves .cache/ (Go build and
# module caches), .probe/ and .evolve/ alone: those are local state, not build
# artifacts.
clean() {
	rm -rf "$DIST" build
	rm -f zeokeel zeokeel.exe
	find cmd internal -type f -name '*.test' -delete 2>/dev/null || true
	echo "removed: $DIST/ build/ zeokeel zeokeel.exe cmd|internal *.test"
}

case "${1:-native}" in
native) build_native ;;
linux-amd64 | linux/amd64) build_one linux amd64 ;;
linux-arm64 | linux/arm64) build_one linux arm64 ;;
darwin-amd64 | darwin/amd64) build_one darwin amd64 ;;
darwin-arm64 | darwin/arm64) build_one darwin arm64 ;;
windows | windows-amd64 | windows/amd64) build_windows ;;
all)
	build_one linux amd64
	build_one linux arm64
	build_one darwin arm64
	build_windows
	;;
package-windows) package_windows ;;
clean) clean ;;
check) go build ./... ;;
help | -h | --help) usage ;;
*) usage; exit 2 ;;
esac
