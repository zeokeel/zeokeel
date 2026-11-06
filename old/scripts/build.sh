#!/bin/sh
# Canonical build and packaging entry point.
#
# Every binary is written to dist/ with the same stable name on every run:
#
#   dist/zeokeel-<goos>-<goarch>[.exe]            one-shot build
#   dist/zeokeel-<goos>-<goarch>-console.exe      Windows console twin only
#   dist/zeokeel-windows-amd64.zip                Windows package
#   dist/zeokeel-linux-<goarch>.tar.gz            Linux package
#   dist/zeokeel-macos-<goarch>.zip               macOS .app package (unsigned)
#   dist/SHA256SUMS.txt                           checksums for the artifacts
#
# Builds carry version metadata injected through -ldflags -X (AR-03):
#   ZEOKEEL_VERSION   defaults to `git describe --tags --always --dirty`
#   ZEOKEEL_COMMIT    defaults to `git rev-parse --short HEAD`
#   ZEOKEEL_BUILD_TIME defaults to the current UTC time
# and are stripped (-s -w, ~32% smaller binaries). Set ZEOKEEL_VERSION=1.2.3
# for a release build.
#
# Usage:
#   sh scripts/build.sh                 # host binary (native)
#   sh scripts/build.sh linux-amd64     # one target: linux-amd64|linux-arm64|darwin-amd64|darwin-arm64
#   sh scripts/build.sh windows         # windows/amd64 GUI + console twins
#   sh scripts/build.sh all             # linux/amd64, linux/arm64, darwin/arm64, windows/amd64
#   sh scripts/build.sh package-windows # windows build + dist/zeokeel-windows-amd64.zip
#   sh scripts/build.sh package-linux   # linux/amd64+arm64 + tar.gz packages
#   sh scripts/build.sh package-macos   # darwin/arm64+amd64 + unsigned .app zips
#   sh scripts/build.sh clean           # remove every build output (dist/, stray root binaries, *.test)
#   sh scripts/build.sh check           # compile only: go build ./... (writes nothing)
set -eu
cd "$(dirname "$0")/.."
ROOT=$(pwd)

export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

DIST=dist
WIN_README=packaging/windows/README-WINDOWS.txt
LINUX_README=packaging/linux/README-LINUX.txt
MACOS_README=packaging/macos/README-MACOS.txt
WIN_GUI="$DIST/zeokeel-windows-amd64.exe"
WIN_CONSOLE="$DIST/zeokeel-windows-amd64-console.exe"
WIN_ZIP="$DIST/zeokeel-windows-amd64.zip"
WIN_ICO=logo/zeokeel.ico
WIN_RC=cmd/zeokeel/resource_windows_amd64.rc
WIN_SYSO=cmd/zeokeel/resource_windows_amd64.syso

# Build metadata (AR-03). Everything is overridable for reproducible/release
# builds; the defaults identify the checkout.
git_describe() { git describe --tags --always --dirty 2>/dev/null || true; }
VERSION=${ZEOKEEL_VERSION:-$(git_describe)}
[ -n "$VERSION" ] || VERSION=0.1.0-dev
COMMIT=${ZEOKEEL_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}
BUILD_TIME=${ZEOKEEL_BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
# -s -w strips the symbol table and DWARF (49.5MB -> 33.8MB in the audit);
# -trimpath keeps paths reproducible; the three -X values show up in
# `zeokeel --version`, the log header and crash reports.
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildTime=$BUILD_TIME"

usage() {
	cat <<'EOF'
Usage:
  sh scripts/build.sh [target]

Targets:
  native            host binary                            dist/zeokeel-<host>-<arch>
  linux-amd64       linux/amd64                            dist/zeokeel-linux-amd64
  linux-arm64       linux/arm64                            dist/zeokeel-linux-arm64
  darwin-amd64      darwin/amd64                           dist/zeokeel-darwin-amd64
  darwin-arm64      darwin/arm64                           dist/zeokeel-darwin-arm64
  windows           windows/amd64 GUI + console twins      dist/zeokeel-windows-amd64*.exe
  all               linux/amd64, linux/arm64, darwin/arm64, windows/amd64
  package-windows   windows build + zip                    dist/zeokeel-windows-amd64.zip
  package-linux     linux amd64/arm64 + tar.gz packages    dist/zeokeel-linux-<arch>.tar.gz
  package-macos     darwin arm64/amd64 + unsigned .app zip dist/zeokeel-macos-<arch>.zip
  check             compile only: go build ./...           (writes nothing)
  clean             remove dist/, stray root binaries, *.test

Version metadata (overridable):
  ZEOKEEL_VERSION     default: git describe --tags --always --dirty
  ZEOKEEL_COMMIT      default: git rev-parse --short HEAD
  ZEOKEEL_BUILD_TIME  default: current UTC time (RFC 3339)
EOF
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
	GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/zeokeel
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
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS -H windowsgui" -o "$WIN_GUI" ./cmd/zeokeel
	echo "  $WIN_CONSOLE (console)"
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$WIN_CONSOLE" ./cmd/zeokeel
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

# refresh_sums rewrites dist/SHA256SUMS.txt from every packaged artifact that
# currently exists, so each package target leaves valid checksums behind.
refresh_sums() {
	(
		cd "$DIST"
		set --
		for f in zeokeel-*; do
			[ -f "$f" ] || continue
			set -- "$@" "$f"
		done
		if [ "$#" -gt 0 ]; then
			sha256 "$@" >SHA256SUMS.txt
		fi
	)
}

# stage_dir prints a fresh temporary directory; the caller removes it.
stage_dir() { mktemp -d "${TMPDIR:-/tmp}/zeokeel-stage.XXXXXX"; }

# package_name returns the stable package name for a GOOS/GOARCH pair. The
# artifact names must not change between builds (a release version belongs in
# the metadata, not the file name): dist/zeokeel-linux-amd64.tar.gz,
# dist/zeokeel-macos-arm64.zip.
package_name() { printf 'zeokeel-%s-%s' "$1" "$2"; }

package_windows() {
	command -v zip >/dev/null 2>&1 || die "package-windows needs 'zip' on PATH"
	[ -f "$WIN_README" ] || die "missing $WIN_README"
	build_windows
	cp "$WIN_README" "$DIST/README-WINDOWS.txt"
	(
		cd "$DIST"
		rm -f zeokeel-windows-amd64.zip
		zip -q -X zeokeel-windows-amd64.zip \
			zeokeel-windows-amd64.exe zeokeel-windows-amd64-console.exe \
			README-WINDOWS.txt
	)
	refresh_sums
	echo "  $WIN_ZIP"
}

package_linux() {
	command -v tar >/dev/null 2>&1 || die "package-linux needs 'tar' on PATH"
	[ -f "$LINUX_README" ] || die "missing $LINUX_README"
	for arch in amd64 arm64; do
		bin=$(target_bin linux "$arch")
		build_one linux "$arch"
		name=$(package_name linux "$arch")
		stage=$(stage_dir)
		mkdir -p "$stage/$name"
		cp "$bin" "$stage/$name/zeokeel"
		cp "$LINUX_README" "$stage/$name/README-LINUX.txt"
		(
			cd "$stage"
			tar -czf "$ROOT/$DIST/$name.tar.gz" "$name"
		)
		rm -rf "$stage"
		echo "  $DIST/$name.tar.gz"
	done
	refresh_sums
}

# plist_version renders a CFBundleShortVersionString-safe version: strips a
# leading v and any git-describe suffix, falls back to 0.1.0.
plist_version() {
	v=$(printf '%s' "$VERSION" | sed 's/^v//' | sed 's/[^0-9.].*$//' | sed 's/^\.*//;s/\.*$//')
	[ -n "$v" ] || v=0.1.0
	printf '%s' "$v"
}

package_macos() {
	command -v zip >/dev/null 2>&1 || die "package-macos needs 'zip' on PATH"
	[ -f "$MACOS_README" ] || die "missing $MACOS_README"
	short=$(plist_version)
	for arch in arm64 amd64; do
		name=$(package_name macos "$arch")
		bin=$(target_bin darwin "$arch")
		build_one darwin "$arch"
		stage=$(stage_dir)
		appdir="$stage/ZeoKeel.app"
		mkdir -p "$appdir/Contents/MacOS" "$appdir/Contents/Resources"
		cp "$bin" "$appdir/Contents/MacOS/zeokeel"
		chmod 0755 "$appdir/Contents/MacOS/zeokeel"
		cp "$MACOS_README" "$appdir/Contents/Resources/README-MACOS.txt"
		cat >"$appdir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key><string>en</string>
	<key>CFBundleExecutable</key><string>zeokeel</string>
	<key>CFBundleIdentifier</key><string>com.zeokeel.zeokeel</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>CFBundleName</key><string>ZeoKeel</string>
	<key>CFBundleDisplayName</key><string>ZeoKeel</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$short</string>
	<key>CFBundleVersion</key><string>$COMMIT</string>
	<key>LSMinimumSystemVersion</key><string>13.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
		(
			cd "$stage"
			zip -q -X -r "$ROOT/$DIST/$name.zip" "ZeoKeel.app"
		)
		rm -rf "$stage"
		echo "  $DIST/$name.zip (unsigned; see README-MACOS.txt)"
	done
	refresh_sums
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
package-linux) package_linux ;;
package-macos) package_macos ;;
clean) clean ;;
check) go build ./... ;;
help | -h | --help) usage ;;
*) usage; exit 2 ;;
esac
