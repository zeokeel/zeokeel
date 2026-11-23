#!/usr/bin/env bash
# Copy the built browser client (web/dist) into the gateway's embed directory.
#
# Why a copy: go:embed cannot name a path outside its own package directory, and
# the browser client's source of truth is web/ (owned by the web client track).
# The gateway therefore embeds internal/gateway/webclient/dist, which this script
# fills from web/dist. Nothing is copied when the bundle is missing: the embed
# directory keeps its committed .keep marker, the binary still builds, and
# GET / answers with a readable "client bundle not built" page (503).
#
# The copy is staged in a temporary sibling and renamed into place, so a failed
# or interrupted copy can never leave a half-written dist/ for go:embed to pick
# up. The staged tree also carries BUNDLE_INFO — the fingerprint the gateway logs
# at startup — so "the shipped binary has an old UI" is visible in the log.
#
# Usage:
#   scripts/build-web-client.sh                 # strict copy (error if web/dist is missing)
#   scripts/build-web-client.sh --build         # run `npm run build` in web/ first
#   scripts/build-web-client.sh --if-available  # copy only if web/dist exists (make build)
#   scripts/build-web-client.sh --clean         # remove the copied bundle (keep .keep)
#   scripts/build-web-client.sh --print         # print the embedded bundle fingerprint
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="$root/web/dist"
dest="$root/internal/gateway/webclient/dist"

clean() {
  find "$dest" -mindepth 1 ! -name '.keep' -delete
  echo "cleaned $dest"
}

fingerprint() {
  if [ -f "$dest/BUNDLE_INFO" ]; then
    tr '\n' ' ' <"$dest/BUNDLE_INFO" | sed 's/ $//'
    echo
    return 0
  fi
  echo "no bundle embedded (dist/ holds only .keep)" >&2
  return 1
}

case "${1:-}" in
  --clean) clean; exit 0 ;;
  --print) fingerprint; exit $? ;;
  --build | --if-available | "") ;;
  *) echo "usage: $0 [--build|--if-available|--clean|--print]" >&2; exit 2 ;;
esac

if [ "${1:-}" = "--build" ]; then
  if [ ! -d "$root/web/node_modules" ]; then
    echo "web/node_modules is missing: run 'npm ci' in web/ first" >&2
    exit 1
  fi
  ( cd "$root/web" && npm run build )
fi

if [ ! -f "$src/index.html" ]; then
  if [ "${1:-}" = "--if-available" ]; then
    # Inner-loop builds must not require npm. Say it loudly instead of shipping
    # silently: the gateway logs the same warning at startup.
    echo "warning: web/dist/index.html is missing; the gateway will embed no browser client" >&2
    echo "         (GET / will answer 503 until 'make web-client' runs)" >&2
    exit 0
  fi
  echo "web/dist/index.html is missing." >&2
  echo "Build the client first:  (cd web && npm ci && npm run build)" >&2
  echo "Then re-run:             scripts/build-web-client.sh" >&2
  exit 1
fi

stage="$dest.stage.$$"
trap 'rm -rf "$stage"' EXIT
rm -rf "$stage"
mkdir -p "$stage"
cp -a "$src"/. "$stage"/
touch "$stage/.keep"

# Fingerprint: hashed over the copied files in sorted order. It travels inside
# the binary so the running gateway can report exactly which client build it
# serves (`scripts/build-web-client.sh --print` shows the same text).
file_count="$(cd "$stage" && find . -type f ! -name BUNDLE_INFO | wc -l | tr -d ' ')"
digest="$(cd "$stage" && find . -type f ! -name BUNDLE_INFO -print0 |
  LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)"

# builtAt is the SOURCE mtime, not "now": re-running the copy without rebuilding
# the client must produce a byte-identical tree, or every `make build` would
# change the binary and break reproducible builds.
built_at="$(date -u -r "$src/index.html" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
  date -u -d "@$(stat -c %Y "$src/index.html")" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
  echo unknown)"
{
  echo "schemaVersion 1"
  echo "builtAt $built_at"
  echo "source web/dist"
  echo "files $file_count"
  echo "sha256 $digest"
} >"$stage/BUNDLE_INFO"

# Nothing to do when the fingerprint matches what is already embedded: keeps
# `make build` from touching the embed directory on every run.
if [ -f "$dest/BUNDLE_INFO" ] && cmp -s "$stage/BUNDLE_INFO" "$dest/BUNDLE_INFO"; then
  echo "embedded browser client unchanged: $file_count files, sha256 $digest"
  exit 0
fi

# Swap it in only now that the staged tree is complete.
rm -rf "$dest"
mv "$stage" "$dest"
echo "embedded browser client: $file_count files, sha256 $digest"
echo "  source $src -> $dest"