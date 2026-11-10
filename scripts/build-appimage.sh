#!/usr/bin/env bash
# Build a Linux AppImage from a `go tool mygo build -platform linux/<arch>` output.
#
# Usage (the release workflow's interface):
#   bash scripts/build-appimage.sh build/linux-amd64 0.2.2 amd64
#   bash scripts/build-appimage.sh build/linux-arm64 0.2.2 arm64
#
# <linux-build-dir> is the directory mygo wrote for Linux: the `zeokeel`
# executable, `zeokeel.desktop`, `zeokeel.png` and the app's resources. The
# script stages it as an AppDir (adding `AppRun` -> `zeokeel` and `.DirIcon`),
# leaves the install artifacts of the directory out (Debian package, tarball,
# PKGBUILD, install.sh, update manifests, .mygo-files), and runs appimagetool.
#
# Output: build/zeokeel-<version>-linux-<arch>.AppImage (executable).
#
# Environment:
#   APPIMAGETOOL        appimagetool executable (default: `appimagetool` in PATH)
#   APPIMAGE_RUNTIME    AppImage runtime for the architecture. Default:
#                       ../share/AppImageKit/runtime/runtime-<appimage-arch>
#                       next to appimagetool, e.g.
#                       /usr/share/AppImageKit/runtime/runtime-x86_64.
#                       The go-appimage appimagetool also accepts APPIMAGE_RUNTIME
#                       as a runtime-<arch> file beside a staged copy of itself.
#   APPIMAGE_TOOL_STYLE go-appimage or appimagekit, when `--help` cannot tell
#                       (normally detected: go-appimage's Go appimagetool takes
#                       one argument, AppImageKit's C tool takes two)
#   APPIMAGE_RUNTIME_FLAG  AppImageKit builds only: how the runtime option is
#                       spelled (usually --runtime-file, older builds -r)
#   APPIMAGE_DRY_RUN=1  assemble and list the AppDir under
#                       build/.appimage-dry-run-<appimage-arch>/, run no tool
#   TMPDIR              where the AppDir is staged (default: /tmp)
#
# Requirements: appimagetool from go-appimage
# (github.com/probonopd/go-appimage/src/appimagetool, as CI installs), and
# mksquashfs, desktop-file-validate, patchelf and file in PATH. The Go
# appimagetool also requires `uploadtool` in PATH (CI provides a dummy that
# does nothing; the release workflow publishes the files itself) and reads
# $VERSION and $ARCH, which this script sets.
set -euo pipefail

die() {
	printf 'build-appimage: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<'EOF'
usage: scripts/build-appimage.sh <linux-build-dir> <version> <arch>

  <linux-build-dir>  output of `go tool mygo build -platform linux/<arch>`,
                     e.g. build/linux-amd64 (binary zeokeel, .desktop, icon)
  <version>          app version, e.g. 0.2.2 (from VERSION)
  <arch>             Go architecture: amd64 or arm64

environment: APPIMAGETOOL, APPIMAGE_RUNTIME, APPIMAGE_RUNTIME_FLAG,
             APPIMAGE_TOOL_STYLE, APPIMAGE_DRY_RUN=1, TMPDIR
EOF
}

[ $# -eq 3 ] || {
	usage
	exit 2
}

build_dir_arg=$1
version=$2
goarch=$3

case "$goarch" in
amd64) appimage_arch=x86_64 ;;
arm64) appimage_arch=aarch64 ;;
*)
	die "unsupported architecture \"$goarch\" (use amd64 or arm64)"
	;;
esac

[ -n "$version" ] || die "version is empty"
case "$version" in
*[!0-9A-Za-z.+_-]*)
	die "version \"$version\" is not a plain version (letters, digits, . + _ - only)"
	;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Relative build directories are relative to the repository root, which is the
# release workflow's working directory.
case "$build_dir_arg" in
/*) build_dir=$build_dir_arg ;;
*) build_dir="$ROOT/$build_dir_arg" ;;
esac

[ -d "$build_dir" ] || die "no build directory: $build_dir (build it with scripts/build-app.sh -platform linux/$goarch first)"
[ -f "$build_dir/zeokeel" ] || die "$build_dir has no zeokeel binary (not a mygo Linux build?)"

# Stage the AppDir, keeping only what an AppImage ships.
dry_run=0
if [ "${APPIMAGE_DRY_RUN:-0}" = "1" ]; then
	dry_run=1
	stage_root="$ROOT/build/.appimage-dry-run-$appimage_arch"
	rm -rf "$stage_root"
	mkdir -p "$stage_root"
else
	stage_root="$(mktemp -d "${TMPDIR:-/tmp}/zeokeel-appimage.XXXXXX")"
	trap 'rm -rf "$stage_root"' EXIT
fi
appdir="$stage_root/zeokeel-${version}-${appimage_arch}.AppDir"
mkdir -p "$appdir"

# Everything mygo wrote, hidden files included (.mygo-files is removed below).
cp -R "$build_dir/." "$appdir/"

# Packaging artifacts of the build directory belong to the .deb/.tar.gz/
# PKGBUILD/install.sh channels, not inside the AppImage.
for pattern in '*.deb' '*.tar.gz' '*.zip' 'PKGBUILD' 'install.sh' 'update-*.json' '.mygo-files'; do
	rm -f "$appdir"/$pattern
done

[ -f "$appdir/zeokeel" ] || die "AppDir is missing the zeokeel binary"
[ -f "$appdir/zeokeel.desktop" ] || die "AppDir is missing zeokeel.desktop"
[ -f "$appdir/zeokeel.png" ] || die "AppDir is missing zeokeel.png"
chmod 0755 "$appdir/zeokeel"

# AppImage conventions: AppRun is the entry point, and the icon named by the
# desktop entry is also the directory icon.
ln -sf zeokeel "$appdir/AppRun"
ln -sf zeokeel.png "$appdir/.DirIcon"

output="$ROOT/build/zeokeel-${version}-linux-${goarch}.AppImage"

if [ "$dry_run" = "1" ]; then
	echo "build-appimage: dry run: AppDir assembled, appimagetool not called"
	echo "build-appimage: AppDir: $appdir"
	(
		cd "$appdir"
		echo "build-appimage: contents:"
		find . -print | LC_ALL=C sort | sed 's/^/  /'
	)
	echo "build-appimage: would write: $output"
	exit 0
fi

# appimagetool: from the environment or PATH.
tool="${APPIMAGETOOL:-}"
if [ -z "$tool" ]; then
	tool="$(command -v appimagetool || true)"
fi
[ -n "$tool" ] || die "appimagetool not found — set APPIMAGETOOL or install go-appimage's appimagetool"
[ -x "$tool" ] || die "appimagetool is not executable: $tool"
tool_dir="$(cd "$(dirname "$tool")" && pwd)"

# go-appimage's appimagetool looks for uploadtool in PATH and exits without
# it; CI keeps a dummy beside appimagetool, so put that directory first.
if ! command -v uploadtool >/dev/null 2>&1 && [ -x "$tool_dir/uploadtool" ]; then
	PATH="$tool_dir:$PATH"
	export PATH
fi
if ! command -v uploadtool >/dev/null 2>&1; then
	echo "build-appimage: warning: uploadtool not in PATH (the go-appimage appimagetool requires it; CI provides a dummy)" >&2
fi
for need in mksquashfs desktop-file-validate patchelf file; do
	command -v "$need" >/dev/null 2>&1 ||
		die "$need not found in PATH (appimagetool calls it)"
done

# The runtime file: APPIMAGE_RUNTIME, else AppImageKit's layout beside
# appimagetool. The go-appimage appimagetool discovers runtime-<arch> beside
# its own executable, or in ../share/AppImageKit/runtime/, by itself.
runtime="${APPIMAGE_RUNTIME:-}"
if [ -z "$runtime" ]; then
	runtime="$tool_dir/../share/AppImageKit/runtime/runtime-$appimage_arch"
fi

# Two appimagetool lineages need two invocations:
#   go-appimage (the Go rewrite CI installs): `appimagetool <AppDir>`; it
#     writes <Name>-<version>-<arch>.AppImage into the current directory,
#     takes its runtime from beside the executable, names the version from
#     $VERSION and the architecture from $ARCH, and aborts on unknown flags.
#   AppImageKit (the C tool): `appimagetool [-n] [-r runtime] <AppDir> <output>`.
style="${APPIMAGE_TOOL_STYLE:-}"
help_text="$("$tool" --help 2>&1 || true)"
if [ -z "$style" ]; then
	case "$help_text" in
	*runtime-file* | *"-r "* | *"-r,"*) style=appimagekit ;;
	*) style=go-appimage ;;
	esac
fi

mkdir -p "$ROOT/build"
rm -f "$output"

case "$style" in
go-appimage)
	# The tool finds runtime-<arch> beside its executable; stage a copy of the
	# tool and the runtime when APPIMAGE_RUNTIME names one somewhere else.
	go_tool=$tool
	if [ ! -f "$tool_dir/runtime-$appimage_arch" ] &&
		[ ! -f "$tool_dir/../share/AppImageKit/runtime/runtime-$appimage_arch" ]; then
		[ -f "$runtime" ] ||
			die "appimagetool expects its runtime at $tool_dir/runtime-$appimage_arch; put one there, or set APPIMAGE_RUNTIME to stage it"
		mkdir -p "$stage_root/tool"
		cp "$tool" "$stage_root/tool/appimagetool"
		chmod 0755 "$stage_root/tool/appimagetool"
		cp "$runtime" "$stage_root/tool/runtime-$appimage_arch"
		chmod 0755 "$stage_root/tool/runtime-$appimage_arch"
		go_tool="$stage_root/tool/appimagetool"
	fi
	out_dir="$stage_root/out"
	rm -rf "$out_dir"
	mkdir -p "$out_dir"
	echo "build-appimage: $go_tool"
	echo "build-appimage: AppDir: $appdir"
	echo "build-appimage: output: $output"
	# GITHUB_REPOSITORY/GITHUB_REF make the tool embed AppImageUpdate metadata
	# guessed from the release asset names (ZeoKeel-*-x86_64.AppImage.zsync),
	# which this project does not publish; leave them out so the build embeds
	# only the deterministic digest.
	(
		cd "$out_dir"
		env -u GITHUB_REPOSITORY -u GITHUB_REF \
			VERSION="$version" ARCH="$appimage_arch" "$go_tool" "$appdir"
	)
	found=("$out_dir"/*.AppImage)
	[ -e "${found[0]}" ] || die "appimagetool wrote no AppImage into $out_dir"
	[ "${#found[@]}" -eq 1 ] || die "appimagetool wrote more than one file into $out_dir"
	mv -f "${found[0]}" "$output"
	;;
appimagekit)
	# How the C appimagetool spells its runtime option: --runtime-file in
	# newer releases, -r in older ones, -runtime-file in Go-flag builds.
	runtime_flag="${APPIMAGE_RUNTIME_FLAG:-}"
	if [ -z "$runtime_flag" ]; then
		case "$help_text" in
		*--runtime-file*) runtime_flag="--runtime-file" ;;
		*-runtime-file*) runtime_flag="-runtime-file" ;;
		*"-r,"* | *"-r "*) runtime_flag="-r" ;;
		esac
	fi
	runtime_args=()
	if [ -n "$runtime_flag" ]; then
		[ -f "$runtime" ] ||
			die "no AppImage runtime at $runtime for $appimage_arch — set APPIMAGE_RUNTIME"
		runtime_args=("$runtime_flag" "$runtime")
	fi
	# Older releases require AppStream metadata unless told to skip it; mygo
	# writes none for an app without file types, so skip when supported.
	no_appstream=()
	case "$help_text" in
	*--no-appstream*) no_appstream=(--no-appstream) ;;
	esac
	echo "build-appimage: $tool"
	echo "build-appimage: AppDir: $appdir"
	echo "build-appimage: output: $output"
	# ${arr[@]+...} keeps bash 3.2 (macOS) from failing on an empty array
	# under set -u.
	ARCH="$appimage_arch" "$tool" \
		${no_appstream[@]+"${no_appstream[@]}"} \
		${runtime_args[@]+"${runtime_args[@]}"} \
		"$appdir" "$output"
	;;
*)
	die "unknown APPIMAGE_TOOL_STYLE \"$style\" (use go-appimage or appimagekit)"
	;;
esac

[ -f "$output" ] || die "appimagetool did not write $output"
chmod +x "$output"
printf 'build-appimage: wrote %s (%s bytes)\n' "$output" "$(wc -c <"$output" | tr -d ' ')"
