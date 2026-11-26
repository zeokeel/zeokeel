#!/usr/bin/env bash
# Archive the release binary for the current GOOS/GOARCH into dist/.
#
# One binary is the whole product (serve / headless / acp) with the browser
# client embedded, so the artifact is a tarball (or a zip on Windows) plus the
# version in its name. No installer, no signature, no notarization: there is
# nothing platform-specific left to package.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

version="$(cat VERSION)"
goos="${GOOS:-$(go env GOOS)}"
goarch="${GOARCH:-$(go env GOARCH)}"
binary="zeokeel"
[ "$goos" = "windows" ] && binary="zeokeel.exe"

[ -x "dist/${binary}" ] || { echo "release-archive: dist/${binary} is missing" >&2; exit 1; }

name="zeokeel-${version}-${goos}-${goarch}"
if [ "$goos" = "windows" ]; then
  (cd dist && zip -q -j "${name}.zip" "${binary}")
  echo "release-archive: dist/${name}.zip"
else
  tar -C dist -czf "dist/${name}.tar.gz" "${binary}"
  echo "release-archive: dist/${name}.tar.gz"
fi
