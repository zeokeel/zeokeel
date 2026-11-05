#!/bin/sh
# ZeoKeel CI quality gate. One command = the definition of "green".
# Usage: sh scripts/ci.sh [--fast]
#
# GitHub Actions runs this script in .github/workflows/ci.yml (job "gate"), so
# local and CI results are the same command. `-race ./internal/shell` costs
# ~9 minutes and runs as a separate workflow job instead (AR-02).
# Coverage is written to .cache/coverage.out (uploaded as a CI artifact); it is
# recorded, not gated on a number yet (see docs/BUGS.md AR-17).
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

# DSH pixel-baseline gate retired 2026-11-05 (docs/decisions/0001-retire-dsh-pixel-parity.md).
# Visual quality is now owned by the ZeoKeel design system, not by DSH screenshot diffs.

if [ "$FAST" = 0 ]; then
  step "coverage (profile + summary)"
  mkdir -p .cache
  go test -count=1 -coverprofile=.cache/coverage.out -covermode=atomic ./internal/... 2>/dev/null \
    | awk '/coverage:/ {print}' | sort -t: -k2 -n | tail -20
  if [ -f .cache/coverage.out ]; then
    echo "total: $(go tool cover -func=.cache/coverage.out | tail -1)"
  fi

  # Blocking gate: a race failure fails CI. Only an absent cgo toolchain skips
  # the step, since -race needs cgo to build its runtime.
  step "race (blocking; needs a cgo toolchain)"
  race_cc=$(go env CC 2>/dev/null || echo gcc)
  race_cc=${race_cc%% *}
  if [ -n "$race_cc" ] && command -v "$race_cc" >/dev/null 2>&1; then
    if ! CGO_ENABLED=1 go test -race -count=1 ./internal/state ./internal/store ./internal/session ./internal/agentteam ./internal/tools ./internal/runtime ./internal/app 2>/tmp/zk-race.log; then
      tail -20 /tmp/zk-race.log
      fail "race"
    fi
  else
    echo "race skipped: no cgo toolchain (CC=${race_cc:-unset})"
  fi
fi

echo
echo "CI PASS"
