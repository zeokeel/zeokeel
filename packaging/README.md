# Packaging

Release packaging is produced by `scripts/build.sh` (and the matching `make`
targets). Every package lands in `dist/` with a stable name and a
`dist/SHA256SUMS.txt` checksum file.

| Target | Artifacts | Docs |
| --- | --- | --- |
| `make package-windows` | `dist/zeokeel-windows-amd64.zip` (GUI `zeokeel-windows-amd64.exe` + console twin `-console.exe` + `README-WINDOWS.txt`) | [windows/README-WINDOWS.txt](windows/README-WINDOWS.txt) |
| `make package-linux` | `dist/zeokeel-linux-amd64.tar.gz`, `dist/zeokeel-linux-arm64.tar.gz` (binary + `README-LINUX.txt` inside a versioned directory) | [linux/README-LINUX.txt](linux/README-LINUX.txt) |
| `make package-macos` | `dist/zeokeel-macos-arm64.zip`, `dist/zeokeel-macos-amd64.zip` (unsigned `ZeoKeel.app` bundle) | [macos/README-MACOS.txt](macos/README-MACOS.txt) |

## Build metadata

All targets inject version metadata with `-ldflags -X` and strip the binary
(`-s -w`, ~32% smaller; see AR-03/AR-21 in `docs/BUGS.md`):

    ZEOKEEL_VERSION=1.2.3 ZEOKEEL_COMMIT=abc1234 make package-linux

Defaults: `git describe --tags --always --dirty`, `git rev-parse --short HEAD`
and the current UTC time. `zeokeel --version` prints all three, and the same
line is written to the log header and crash reports.

## Requirements

* `zip` for Windows/macOS packages, `tar` for Linux packages, `sha256sum`
  (or `shasum`) for checksums.
* `x86_64-w64-mingw32-windres` (MinGW binutils) is optional: the icon resource
  is committed as `cmd/zeokeel/resource_windows_amd64.syso`, so icon-bearing
  Windows builds work without MinGW; the tool is only needed to regenerate it
  after changing `logo/zeokeel.ico` / the `.rc` file.

## Deferred (needs infra outside this repo)

* Code signing / notarization (Windows Authenticode, macOS Developer ID) —
  requires certificates and CI secrets.
* Auto-update (feed URL, signatures, rollback).
* MSI/NSIS installer for Windows: the current zip is intentionally portable
  (extract and run) and avoids an installer dependency. Installer work is
  tracked in `docs/BUGS.md` (AR-03).
