#!/bin/sh
# Regenerate the Windows executable icon resource from logo/zeokeel.ico.
#
# The generated cmd/zeokeel/resource_windows_amd64.syso is committed so normal
# builds (and machines without MinGW binutils) still get the icon. Run this
# script after changing logo/zeokeel.ico or the .rc file; scripts/build.sh calls
# it automatically when the source is newer than the .syso and windres exists.
set -eu
cd "$(dirname "$0")/.."

WINDRES="${WINDRES:-x86_64-w64-mingw32-windres}"
command -v "$WINDRES" >/dev/null 2>&1 || {
	echo "gen-windows-resource: $WINDRES not found (install mingw-w64 binutils)" >&2
	exit 1
}
[ -f logo/zeokeel.ico ] || { echo "gen-windows-resource: missing logo/zeokeel.ico" >&2; exit 1; }
[ -f cmd/zeokeel/resource_windows_amd64.rc ] || { echo "gen-windows-resource: missing resource .rc" >&2; exit 1; }

echo "== regenerating cmd/zeokeel/resource_windows_amd64.syso"
# Run from the package directory so the relative icon path in the .rc resolves.
(cd cmd/zeokeel && "$WINDRES" -i resource_windows_amd64.rc -o resource_windows_amd64.syso -O coff)
ls -l cmd/zeokeel/resource_windows_amd64.syso
