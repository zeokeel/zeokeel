#!/usr/bin/env bash
# Run the pinned Ant Design CLI (@ant-design/cli) with sandbox-safe config dirs.
#
# Why this wrapper exists: the CLI writes its config to $XDG_CONFIG_HOME/antd-cli-nodejs
# (defaults to ~/.config), which is read-only in this workspace sandbox. Redirecting HOME /
# XDG_* under .cache keeps it working without touching files outside the repo.
#
# Usage:
#   bash scripts/antd-cli.sh --install          # install/refresh the CLI into .cache/antd-cli
#   bash scripts/antd-cli.sh list --version 6 --format json
#   bash scripts/antd-cli.sh info Button --version 6 --format json
#   bash scripts/antd-cli.sh demo Select basic --format json
#   bash scripts/antd-cli.sh token Table --version 6 --format json
#   bash scripts/antd-cli.sh semantic Modal --version 6 --format json
#   bash scripts/antd-cli.sh doc Table --lang zh --format json
#   bash scripts/antd-cli.sh lint ./web/src --format json
#   bash scripts/antd-cli.sh usage ./web/src --format json
#   bash scripts/antd-cli.sh mcp                # MCP server (8 tools, 2 prompts)
#
# All metadata is bundled (antd v4/v5/v6) — queries work fully offline.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base="$root/.cache/antd-cli"
bin="$base/node_modules/.bin/antd"

if [[ "${1:-}" == "--install" ]]; then
  mkdir -p "$base" "$root/.cache/npm-cache"
  npm i @ant-design/cli --prefix "$base" --cache "$root/.cache/npm-cache" --no-audit --no-fund --loglevel=error
  echo "installed: $("$bin" -V)"
  exit 0
fi

if [[ ! -x "$bin" ]]; then
  echo "antd CLI not installed. Run: bash scripts/antd-cli.sh --install" >&2
  exit 1
fi

export XDG_CONFIG_HOME="$base/config"
export XDG_CACHE_HOME="$base/xdg-cache"
export XDG_DATA_HOME="$base/xdg-data"
export HOME="$base/home"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$XDG_DATA_HOME" "$HOME"

exec "$bin" "$@"