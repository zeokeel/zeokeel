#!/usr/bin/env bash
# Build, sign and (optionally) notarize a macOS DMG on Linux.
#
# The Go code is compiled on Linux (no cgo), so no macOS runner is needed:
#   - libdmg-hfsplus (mkfs.hfsplus/hfsplus/dmg) creates the UDIF image;
#   - rcodesign (apple-codesign) signs the .app and the dmg, submits the dmg
#     to Apple's notary service and staples the ticket. All three tools run on
#     Linux; notarization only needs the Apple App Store Connect API key.
#
# Usage: package-macos-dmg.sh <ZeoKeel.app> <output.dmg> [volume-name]
#
# Tools (PATH or environment):
#   MKFS_HFSPLUS_TOOL  mkfs.hfsplus from hfsprogs
#   HFSPLUS_TOOL       hfsplus from libdmg-hfsplus
#   DMG_TOOL           dmg from libdmg-hfsplus
#   RCODESIGN          rcodesign from apple-codesign
#
# Signing/notary (optional; unset produces an unsigned dmg):
#   MACOS_P12_FILE, MACOS_CERT_PASSWORD   Developer ID .p12 + password
#   APPLE_API_KEY_P8_FILE, APPLE_API_KEY_ID, APPLE_API_ISSUER
#                                         App Store Connect API key for notary
set -euo pipefail

app=${1:?usage: package-macos-dmg.sh <ZeoKeel.app> <output.dmg> [volume-name]}
out=${2:?usage: package-macos-dmg.sh <ZeoKeel.app> <output.dmg> [volume-name]}
volume=${3:-ZeoKeel}
mkfs=${MKFS_HFSPLUS_TOOL:-mkfs.hfsplus}
hfsplus=${HFSPLUS_TOOL:-hfsplus}
dmg=${DMG_TOOL:-dmg}
rcodesign=${RCODESIGN:-rcodesign}

[[ -d "$app" ]] || { echo "no app bundle at $app" >&2; exit 1; }
if ! command -v "$mkfs" >/dev/null; then
  for candidate in /sbin/mkfs.hfsplus /usr/sbin/mkfs.hfsplus; do
    if [[ -x "$candidate" ]]; then mkfs="$candidate"; break; fi
  done
fi
command -v "$mkfs" >/dev/null || { echo "missing $mkfs (apt install hfsprogs)" >&2; exit 1; }
command -v "$hfsplus" >/dev/null || { echo "missing $hfsplus (libdmg-hfsplus)" >&2; exit 1; }
command -v "$dmg" >/dev/null || { echo "missing $dmg (libdmg-hfsplus)" >&2; exit 1; }
mkdir -p "$(dirname "$out")"

work=$(mktemp -d "${TMPDIR:-/tmp}/zeokeel-dmg.XXXXXX")
trap 'rm -rf "$work"' EXIT
stage="$work/stage"
mkdir -p "$stage"
# ditto on macOS preserves what HFS needs; cp -a is the Linux equivalent and
# keeps executable bits and symlinks of the bundle.
cp -a "$app" "$stage/ZeoKeel.app"
chmod -R a+rX,a-st,u+w,go-w "$stage"

sign_args=()
if [[ -n "${MACOS_P12_FILE:-}" ]]; then
  command -v "$rcodesign" >/dev/null || { echo "missing $rcodesign" >&2; exit 1; }
  sign_args=(--p12-file "$MACOS_P12_FILE" --p12-password "${MACOS_CERT_PASSWORD:-}")
  echo "signing the app bundle with Developer ID"
  "$rcodesign" sign "${sign_args[@]}" --code-signature-flags runtime \
    "$stage/ZeoKeel.app"
else
  echo "warning: MACOS_P12_FILE is not set; the app and dmg stay unsigned" >&2
fi

# HFS+ image sized from the staged tree with headroom for HFS metadata.
size_mb=$(du -sm "$stage" | cut -f1)
size_mb=$((size_mb + 32))
hfs="$work/staged.hfs"
dd if=/dev/zero of="$hfs" bs=1M count="$size_mb" status=none
# mkfs.hfsplus follows the macOS mkfs/newfs_hfs interface: -v <volume> <image>.
"$mkfs" -v "$volume" "$hfs" >/dev/null
"$hfsplus" "$hfs" addall "$stage" >/dev/null
# /Applications link next to the app, as the macOS Finder layout expects.
"$hfsplus" "$hfs" symlink /Applications /Applications >/dev/null
"$dmg" build "$hfs" "$out" >/dev/null 2>&1

if ((${#sign_args[@]})); then
  echo "signing the dmg"
  "$rcodesign" sign "${sign_args[@]}" "$out"
fi

if [[ -n "${APPLE_API_KEY_P8_FILE:-}" && -n "${APPLE_API_KEY_ID:-}" && -n "${APPLE_API_ISSUER:-}" ]]; then
  command -v "$rcodesign" >/dev/null || { echo "missing $rcodesign" >&2; exit 1; }
  creds="$work/notary-api-key.json"
  "$rcodesign" encode-app-store-connect-api-key \
    "$APPLE_API_ISSUER" "$APPLE_API_KEY_ID" "$APPLE_API_KEY_P8_FILE" \
    --output-path "$creds"
  echo "submitting the dmg to Apple's notary service"
  "$rcodesign" notary-submit --api-key-path "$creds" --staple "$out"
else
  echo "warning: Apple notary API key is not set; skipping notarization" >&2
fi

"$rcodesign" print-signature-info "$out" >/dev/null 2>&1 || true
ls -lh "$out"
