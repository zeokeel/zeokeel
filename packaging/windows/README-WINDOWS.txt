ZeoKeel — Windows x64 test build (pure Go, CGO_ENABLED=0)

Files
- zeokeel-windows-amd64.exe          GUI build (no console window); recommended
- zeokeel-windows-amd64-console.exe  same app with a console window for logs
- README-WINDOWS.txt

Checksums: SHA256SUMS.txt is published next to the zip (dist/SHA256SUMS.txt and
as a GitHub release asset), not inside the zip. The GitHub release also attaches
the two bare .exe files next to the zip, so either form can be downloaded.

Requirements
- Windows 10/11 x64. No runtime or DLL dependency; single executable.

Run
1. Unzip anywhere and run zeokeel-windows-amd64.exe.
2. This build is unsigned: SmartScreen may warn. Choose "More info" -> "Run anyway".
3. Data (SQLite history/settings) lives in %USERPROFILE%\.zeokeel
   Override with ZEOKEEL_DATA_DIR if desired.
4. Configure the model in Settings -> Models: add Base URL, Model and API key.
   The app reads it from the local settings database; keys are not bundled.

What to test
- Five screens: Home / Session / Plugins / Settings / Automation.
- Live chat (needs a working API endpoint + key entered in Settings -> Models),
  streaming, tool calls, approvals (Allow / Always allow / Deny).
- Chat transcript order: each turn shows user -> tool process block (summary +
  cards) -> final answer -> completion row; composer permission / model menus
  and other popovers use the brand menu style.
- Plan mode: type /plan in the composer (or pick it from the "/" palette); the
  composer shows a plan badge and clicking it (or /plan off) leaves the mode.
  Plan mode is guidance, not a sandbox: combine it with Read Only access for a
  forced read-only planning session.
- Shell: the model sees the PowerShell tool (pwsh) only; there is no bash tool.
  Right sidebar Files/Terminal (terminal is pipe-backed, pwsh first, then cmd).
- Tray icon, single instance, close-to-hide. No native window menu bar.
- Settings persistence: theme, language zh/en, font size, plugins, automations.

Known limits in this build
- Unsigned; no bundled installer yet. The package is a portable zip; an
  Inno Setup / NSIS installer is planned but not built (see
  INSTALLER-PLAN.md in the repository).
- Auto-update: the update engine, the `zeokeel update` CLI and
  Settings -> Updates (check / download / install on restart) are delivered.
  The hosted update feed and signing are deferred, so update checks need a
  configured feed URL (`update.feed_url` / ZEOKEEL_UPDATE_FEED).
- Shell is platform gated: Windows exposes the pwsh tool, POSIX builds expose
  bash; no Git Bash/WSL integration. Terminal on Windows is pipe-backed
  (pty=false); Linux/macOS builds use bash + a real PTY.
- No vision/tool-calling guarantee for arbitrary endpoints; use a tool-capable,
  image-capable model to exercise multimodal input.
