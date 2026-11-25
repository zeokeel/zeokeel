#!/usr/bin/env bash
# Boot the REAL product and prove its own behaviour — no mocks, no fake gateway.
#
# What it does:
#   1. builds cmd/zeokeel into .cache/zkverify-live (the binary under test);
#   2. makes a fresh data dir .cache/verify-live/<UTC timestamp>/ and seeds
#      webhook/sources.json, so the signed inbound path is configured the same
#      way a real deployment would configure it (no WS or settings round-trip
#      needed to get there);
#   3. starts `--product serve` on that data dir, waits (up to 30s) for
#      gateway.json and reads the real port + token from it;
#   4. runs scripts/verify-live/checks.mjs against that port/token over the
#      product's own WebSocket control plane and signed HTTP ingress;
#   5. stops the process, prints the data dir and exits with the checks' code.
#
# Everything the checks print is the product's raw answer. A green run means the
# binary in .cache/zkverify-live said so, not that this script assumed it.
#
# Usage: scripts/verify-live/run.sh
# Exit: 0 = every check PASSed; non-zero = at least one FAIL (or boot failure).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

# Go toolchain for this sandbox (GOPATH/GOCACHE/GOTMPDIR outside the repo).
if [[ -f "$root/.cache/env.sh" ]]; then
  # shellcheck disable=SC1091
  source "$root/.cache/env.sh"
fi

binary="$root/.cache/zkverify-live"
mkdir -p "$root/.cache"

echo "== build =="
go build -o "$binary" ./cmd/zeokeel
echo "built $binary"

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
dir="$root/.cache/verify-live/$stamp"
if [[ -e "$dir" ]]; then
  dir="$dir-$$"
fi
mkdir -p "$dir/webhook"

# One signed webhook source: base64 of the shared secret is the on-disk form
# (internal/modules/webhook/admin.go decodes it back to raw bytes).
secret="$(printf %s 'verify-secret-0123456789' | base64 | tr -d '\n')"
printf '{"version":1,"sources":[{"id":"verify","secret":"%s","channel":"verify"}]}\n' \
  "$secret" >"$dir/webhook/sources.json"

log="$dir/product.log"
pid=""

cleanup() {
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 40); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.25
    done
    if kill -0 "$pid" 2>/dev/null; then
      echo "warning: process $pid did not stop on SIGTERM; killing" >&2
      kill -KILL "$pid" 2>/dev/null || true
    fi
    wait "$pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

echo "== start =="
echo "data dir: $dir"
"$binary" --product serve --data-dir "$dir" >"$log" 2>&1 &
pid=$!

info="$dir/gateway.json"
ready=""
for _ in $(seq 1 60); do
  if [[ -f "$info" ]]; then
    ready="yes"
    break
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "FAIL product exited before writing gateway.json" >&2
    tail -n 40 "$log" >&2 || true
    exit 1
  fi
  sleep 0.5
done
if [[ -z "$ready" ]]; then
  echo "FAIL gateway.json did not appear within 30s" >&2
  tail -n 40 "$log" >&2 || true
  exit 1
fi

read -r port token < <(node -e '
  const fs = require("node:fs");
  const info = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
  process.stdout.write(`${info.port} ${info.token}\n`);
' "$info")

echo "port: $port"
echo "== checks =="
code=0
node "$root/scripts/verify-live/checks.mjs" --port "$port" --token "$token" --dir "$dir" || code=$?

echo "== done =="
echo "data dir: $dir"
echo "product log: $log"
cleanup
pid=""
exit "$code"