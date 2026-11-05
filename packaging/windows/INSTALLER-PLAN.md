# Windows installer — plan (not built or verified in this round)

> Status: **PLAN / DRAFT ONLY**. No installer is produced today; the shipped
> Windows artifact remains the portable `dist/zeokeel-windows-amd64.zip` (GUI +
> console twin + README). This file records the intended shape so the updater
> (`internal/updater/`, TDD RED this round) and packaging can be implemented
> together without redesigning the release layout later.

## Why an installer at all

The zip is portable and needs no admin rights, but it cannot: register an
uninstaller, install a Start-menu shortcut, put the app on PATH, or hand the
updater a stable per-user install location. AR-03 / PD-05 in `docs/BUGS.md`
track the release-chain gap; this plan is the Windows half of it.

## Decision: Inno Setup first, NSIS as fallback

| | Inno Setup | NSIS |
| --- | --- | --- |
| License | free (modified BSD-like) | free (zlib/libpng) |
| Per-user install without admin | first-class (`PrivilegesRequired=lowest`) | possible, more scripting |
| Uninstaller / Add-Remove Programs | built in | built in |
| Code signing | `signtool` on the output | same |
| Tooling on CI | `iscc.exe` (single compiler) | `makensis.exe` |
| Verdict | **primary** — simplest per-user portable-style install and clean upgrade story | fallback if Inno is unavailable on the build host |

Draft scripts live next to this plan and are **not compiled** in this round:
`installer.iss` (Inno) and `zeokeel.nsi` (NSIS). They are intentionally minimal.

## Install layout

- Per-user by default: `%LOCALAPPDATA%\Programs\ZeoKeel\` (no elevation).
  Optional machine-wide mode installs to `%ProgramFiles%\ZeoKeel\` behind
  admin/UAC.
- Files: `zeokeel.exe` (GUI), `zeokeel-console.exe` (console twin), `README-WINDOWS.txt`,
  optional `LICENSE` if one is added later.
- Data stays in `%USERPROFILE%\.zeokeel` (or `ZEOKEEL_DATA_DIR`); the installer
  must never touch it. Uninstall keeps user data by default; "also remove user
  data" is an explicit checkbox.
- Start-menu shortcut + optional "launch at login" that maps to the existing
  `app.autostart` setting (the installer only seeds the shortcut; the app owns
  the setting so both stay consistent).

## Updater integration (contract with `internal/updater`)

1. Installer lays down the app at a stable path and records the install root in
   the registry (`HKCU\Software\ZeoKeel` → `InstallDir`, `Version`) for the
   updater and uninstaller.
2. Auto-update never runs an MSI/EXE installer silently from the network by
   default. The auto-updater (future `internal/updater`) replaces
   `zeokeel.exe` **on restart** via a rename-on-reboot fallback when the file is
   locked (Windows), after verifying the SHA-256 from the release manifest.
3. A downloaded `.exe`/`.msi` installer path is a fallback for major upgrades
   that need new registry/file layout; it must be signed before enabling.
4. Rollback: the updater keeps the previous executable as
   `zeokeel.exe.old-<version>` for one release; the uninstaller and updater both
   understand that name.

## Code signing / notarization (deferred)

Unsigned installers trigger SmartScreen. This needs an Authenticode
certificate + CI secret, so it is **DEFERRED** (docs/BUGS.md AR-03) and blocks
marking the installer "production". Until then the portable zip remains the
supported Windows artifact.

## Build switches (planned)

```sh
# Future, once installed toolchain is available:
iscc packaging/windows/installer.iss /DAppVersion=<version> /DCommit=<sha>
makensis -DAPPVERSION=<version> -DCOMMIT=<sha> packaging/windows/zeokeel.nsi
```

`scripts/build.sh package-windows` stays unchanged this round (zip only); a
`package-windows-installer` target can wrap the compiler above when the
toolchain is added to CI.

## Acceptance for the future implementation

- [ ] Per-user install/uninstall without admin; no data loss on uninstall
      (default), data removal only via explicit checkbox.
- [ ] Installed app starts, `--version` prints the same version as the
      installer/registry, and `zeokeel import`/`zeokeel run` work from the
      install root.
- [ ] Silent install (`/S` for NSIS, `/VERYSILENT` for Inno) usable by IT.
- [ ] Installer respects an existing `ZEOKEEL_DATA_DIR` and never writes
      `%USERPROFILE%\.zeokeel` itself.
- [ ] Signed before being offered as the default download.
