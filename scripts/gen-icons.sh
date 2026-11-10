#!/usr/bin/env bash
# Generate every raster logo/icon from the two official logo SVGs.
#
# The single-source rule (logo/README.md): logo/logo.svg and
# logo/logo-dark.svg are the ONLY logo sources, and this script is the only
# way icons are produced. Outputs, all deterministic:
#
#   assets/icon.png                 1024x1024  logo/logo.svg        (packaging icon)
#   internal/brand/mark.png           48x48    logo/logo.svg        (light UI mark)
#   internal/brand/mark-dark@2x.png   96x96    logo/logo-dark.svg   (dark UI mark, 2x)
#
# No other icon file may be added; extend this script (and logo/README.md)
# instead.
#
# Requirements: rsvg-convert (librsvg2-bin on Debian/Ubuntu, `brew install
# librsvg` on macOS) and file. Offline, no other tooling.
#
# Usage: scripts/gen-icons.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

die() {
	printf 'gen-icons: %s\n' "$*" >&2
	exit 1
}

if ! command -v rsvg-convert >/dev/null 2>&1; then
	die "rsvg-convert not found — install librsvg2-bin (Debian/Ubuntu) or librsvg (macOS: brew install librsvg)"
fi
if ! command -v file >/dev/null 2>&1; then
	die "file not found — install it (Debian/Ubuntu: file; it ships with macOS)"
fi

LIGHT="logo/logo.svg"
DARK="logo/logo-dark.svg"
for src in "$LIGHT" "$DARK"; do
	[ -f "$src" ] || die "missing logo source: $src (see logo/README.md)"
done

# render <source-svg> <size> <output-png>
render() {
	local src=$1 size=$2 out=$3 dir tmp desc
	dir="$(dirname "$out")"
	mkdir -p "$dir"
	tmp="$(mktemp "${TMPDIR:-/tmp}/zeokeel-icon.XXXXXX")"
	# rsvg-convert names the format after the output file, and the temporary
	# name has no extension, so ask for PNG explicitly. A fixed 96 DPI keeps
	# the bytes deterministic across machines.
	if ! rsvg-convert --format=png --dpi-x=96 --dpi-y=96 \
		--width="$size" --height="$size" "$src" -o "$tmp"; then
		rm -f "$tmp"
		die "rsvg-convert failed on $src (${size}x${size})"
	fi
	desc="$(file -b "$tmp")"
	case "$desc" in
	PNG*"${size} x ${size}"*) ;;
	*)
		rm -f "$tmp"
		die "$src did not render to a ${size}x${size} PNG: $desc"
		;;
	esac
	# Replace only when the bytes changed: the go:embed files of
	# internal/brand stay stable for Go's build cache on an unchanged logo.
	if [ -f "$out" ] && cmp -s "$tmp" "$out"; then
		rm -f "$tmp"
	else
		mv -f "$tmp" "$out"
	fi
	printf '  %-34s %4s x %-4s  %s\n' "$out" "$size" "$size" "$(basename "$src")"
}

echo "gen-icons: $(basename "$LIGHT") + $(basename "$DARK")"
render "$LIGHT" 1024 assets/icon.png
render "$LIGHT" 48 internal/brand/mark.png
render "$DARK" 96 "internal/brand/mark-dark@2x.png"
echo "gen-icons: done"
