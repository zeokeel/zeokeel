#!/bin/sh
# Re-run the tests that are still in their TDD RED phase (unimplemented
# features keep the //go:build tdd_red tag so the default gate stays green).
# Exit code 1 is expected while the feature is unimplemented; this script
# prints the failing assertions as the RED evidence.
set -u
cd "$(dirname "$0")/.."
export PATH="${PATH}:/usr/local/go/bin"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off CGO_ENABLED=0
echo "== TDD RED evidence: tests tagged tdd_red (unimplemented features) =="
go test -tags tdd_red -count=1 -v ./internal/runtime 2>&1 | grep -E '^(=== RUN|--- FAIL|--- PASS|FAIL|ok)' || true
