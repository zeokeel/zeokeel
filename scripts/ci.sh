#!/bin/sh
# ZeoKeel CI quality gate. One command = the definition of "green".
# Usage: sh scripts/ci.sh [--fast]
set -eu
cd "$(dirname "$0")/.."

export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

FAST=0
[ "${1:-}" = "--fast" ] && FAST=1

fail() { echo "CI FAIL: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

step "gofmt"
fmt=$(gofmt -l ./cmd ./internal)
[ -z "$fmt" ] || fail "gofmt: $fmt"

step "go vet"
go vet ./... || fail "go vet"

step "go build (CGO_ENABLED=0)"
go build ./... || fail "go build"

step "go test ./... (count=1)"
go test -count=1 ./... || fail "go test"

step "zero-cgo gate"
sh scripts/check-nocgo.sh >/tmp/zk-nocgo.log 2>&1 || { tail -20 /tmp/zk-nocgo.log; fail "check-nocgo"; }

step "app tool invocation"
go test -count=1 ./internal/app -run 'TestProductionToolsInvocable' || fail "tool invocation"

step "pixel baselines (light)"
for s in home session plugins settings automation; do
  SCREEN=$s ARTIFACTS="$PWD/artifacts" go test -count=1 ./internal/shell -run TestScreensBaseline >/tmp/zk-px-$s.log 2>&1 || { tail -20 /tmp/zk-px-$s.log; fail "pixel $s"; }
done

step "pixel baselines (dark/narrow)"
go test -count=1 ./internal/shell -run TestScreensDarkNarrow >/tmp/zk-px-dark.log 2>&1 || { tail -20 /tmp/zk-px-dark.log; fail "dark/narrow"; }

if [ "$FAST" = 0 ]; then
  step "coverage summary"
  go test -count=1 -cover ./internal/... 2>/dev/null | awk '/coverage:/ {print}' | sort -t: -k2 -n | tail -20

  # Blocking gate: a race failure fails CI. Only an absent cgo toolchain skips
  # the step, since -race needs cgo to build its runtime.
  step "race (blocking; needs a cgo toolchain)"
  race_cc=$(go env CC 2>/dev/null || echo gcc)
  race_cc=${race_cc%% *}
  if [ -n "$race_cc" ] && command -v "$race_cc" >/dev/null 2>&1; then
    if ! CGO_ENABLED=1 go test -race -count=1 ./internal/state ./internal/store ./internal/session ./internal/agentteam ./internal/tools ./internal/runtime 2>/tmp/zk-race.log; then
      tail -20 /tmp/zk-race.log
      fail "race"
    fi
  else
    echo "race skipped: no cgo toolchain (CC=${race_cc:-unset})"
  fi
fi

echo
echo "CI PASS"
