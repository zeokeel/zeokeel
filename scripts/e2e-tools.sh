#!/bin/sh
# Real-model tool-call E2E: proves the production registry is callable by the
# model (one live turn must invoke and complete a tool). The key is read from
# DEEPSEEK_API_KEY or the local DSH credentials file and never printed.
set -eu
cd "$(dirname "$0")/.."
KEY="${DEEPSEEK_API_KEY:-}"
if [ -z "$KEY" ]; then
  for f in "$HOME/.dsh/.credentials.yaml" /root/.dsh/.credentials.yaml; do
    [ -f "$f" ] || continue
    KEY=$(awk '/DEEPSEEK_API_KEY:/ {print $2; exit}' "$f" | tr -d '"')
    [ -n "$KEY" ] && break
  done
fi
if [ -z "$KEY" ]; then
  echo "e2e-tools: no DEEPSEEK_API_KEY (env or ~/.dsh/.credentials.yaml); skipping" >&2
  exit 2
fi
export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0
export DEEPSEEK_API_KEY="$KEY"
exec go test -tags e2e -run TestRealModelToolCall -count=1 -v ./internal/app
