package main

// usageText is the `zeokeel --help` / unknown-command output. Keep it short:
// the desktop app is the default command and each subcommand prints its own
// detailed help via its own `--help`.
const usageText = `ZeoKeel — pure-Go desktop AI agent

Usage:
  zeokeel                 launch the desktop app (default)
  zeokeel run [flags] "<prompt>"   run one prompt headless (no window, no tray)
  zeokeel run --help      headless flags: --json, --yes, --model ...
  zeokeel import [flags]  migrate DSH sessions into this data directory
  zeokeel import --help   import flags: --from, --dry-run, --json, --max
  zeokeel acp             run the ACP v1 agent server on stdio (editors/clients)
  zeokeel update [flags]  check/download/install a ZeoKeel update (--check/--download/--install/--json)
  zeokeel --version       print build version, commit and platform
  zeokeel --help          show this help

Environment:
  ZEOKEEL_DATA_DIR        data directory (default: ~/.zeokeel)
  DEEPSEEK_API_KEY        model API key when no provider key is configured

Files:
  <data dir>/logs/app-YYYYMMDD.log    application log (rotated at 8 MiB)
  <data dir>/logs/crash-*.txt         crash report written after a panic
`
