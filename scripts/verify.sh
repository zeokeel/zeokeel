#!/usr/bin/env bash
# One-command verification for ZeoKeel.
#
# Runs the checks from docs/VERIFICATION.md in order and prints a PASS/FAIL line
# with the duration of every step; any failure exits non-zero (the EXIT trap
# still prints the summary). Everything runs offline and caches through the
# local Go module cache.
#
# Usage:
#   scripts/verify.sh            # full: fmt/build/vet/protocol-consistency/test/-race/boundaries/smokes
#   scripts/verify.sh --quick    # skip -race and the product smokes (fast inner loop)
#   scripts/verify.sh --no-smoke # keep -race, skip only the product smokes
#
# Environment (all overridable):
#   GO=/usr/local/go/bin/go  GOPROXY=https://goproxy.cn,direct  GOSUMDB=sum.golang.org  GOFLAGS=-mod=mod
# Extra: VERIFY_KEEP_TEMP=1 keeps the temporary snapshot/data directories.
set -euo pipefail

# Offline toolchain defaults; the caller may override them.
case ":$PATH:" in
*:/usr/local/go/bin:*) ;;
*) export PATH="/usr/local/go/bin:$PATH" ;;
esac
export GO="${GO:-go}"
# gofmt must come from the toolchain Go will actually build with, not from
# whatever `gofmt` happens to be first on PATH: the two disagree across Go
# releases (1.26 vs 1.27 format the same file differently), so a "fmt is clean"
# verdict from the wrong binary is why CI's first step failed on a tag whose
# local gate was green.
export GOFMT="${GOFMT:-$("$GO" env GOROOT)/bin/gofmt}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.org}"
export GOFLAGS="${GOFLAGS:--mod=mod}"

SMOKE=1
RACE=1
for arg in "$@"; do
	case "$arg" in
	--quick)
		RACE=0
		SMOKE=0
		;;
	--no-smoke)
		SMOKE=0
		;;
	-h | --help)
		sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "verify.sh: unknown argument: $arg (want --quick or --no-smoke)" >&2
		exit 2
		;;
	esac
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

passed=0
failed=0
started_at=$(date +%s)
temp_dirs=()

cleanup() {
	if [[ "${VERIFY_KEEP_TEMP:-0}" != "1" ]]; then
		local dir
		for dir in "${temp_dirs[@]}"; do
			if [[ -n "$dir" && -d "$dir" ]]; then
				rm -rf "$dir" || true
			fi
		done
	fi
	return 0
}

finish() {
	local exit_code=$?
	trap - EXIT INT TERM
	local elapsed=$(( $(date +%s) - started_at ))
	echo
	echo "verify summary: ${passed} passed, ${failed} failed, ${elapsed}s total"
	if [[ $exit_code -eq 0 && $failed -eq 0 ]]; then
		echo "verify: PASS"
	else
		echo "verify: FAIL"
	fi
	cleanup || true
	if [[ $failed -gt 0 && $exit_code -eq 0 ]]; then
		exit 1
	fi
	exit "$exit_code"
}
trap finish EXIT INT TERM

step() {
	local name="$1"
	shift
	local started=$(date +%s)
	printf '==> %s ... ' "$name"
	if "$@"; then
		local elapsed=$(( $(date +%s) - started ))
		printf 'PASS (%ss)\n' "$elapsed"
		passed=$((passed + 1))
		return 0
	fi
	local elapsed=$(( $(date +%s) - started ))
	printf 'FAIL (%ss)\n' "$elapsed"
	failed=$((failed + 1))
	return 1
}

run_step() {
	step "$@" || exit 1
}

# run_step_quiet keeps the verbose command output out of the PASS/FAIL line and
# prints the tail only when the step fails.
run_step_quiet() {
	local name="$1"
	shift
	local log
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-step.XXXXXX")
	temp_dirs+=("$log")
	local started
	started=$(date +%s)
	printf '==> %s ... ' "$name"
	if "$@" >"$log" 2>&1; then
		printf 'PASS (%ss)\n' "$(( $(date +%s) - started ))"
		passed=$((passed + 1))
		return 0
	fi
	printf 'FAIL (%ss)\n' "$(( $(date +%s) - started ))"
	failed=$((failed + 1))
	tail -n 60 "$log"
	return 1
}

new_temp_dir() {
	local dir
	dir=$(mktemp -d "${TMPDIR:-/tmp}/zeokeel-verify.XXXXXX")
	temp_dirs+=("$dir")
	printf '%s' "$dir"
}

check_gofmt() {
	local unformatted
	if ! command -v "$GOFMT" >/dev/null 2>&1; then
		echo "gofmt is not installed (set GOFMT=/path/to/gofmt)"
		return 1
	fi
	# Only the module's Go packages: the repository also carries an untracked
	# 35G module cache and a docs/research tree that are not part of the product.
	unformatted=$("$GO" list -f '{{.Dir}}' ./internal/... ./cmd/... | sort -u | while IFS= read -r dir; do
		"$GOFMT" -l "$dir"/*.go 2>/dev/null || true
	done)
	if [[ -n "$unformatted" ]]; then
		echo
		echo "gofmt lists unformatted files:"
		echo "$unformatted"
		return 1
	fi
	return 0
}

# Protocol/implementation consistency: the generated wire contract must declare
# exactly the methods the gateway dispatches (a pure string comparison, so it is
# cheap enough for the quick loop).
check_protocol_consistency() {
	"$GO" test ./internal/gateway -count=1 -run 'TestTransport' >/dev/null
}

# The serve product is the desktop replacement: it must start without a display
# and publish its gateway URL. The old desktop smoke drove a real window under
# Xvfb; there is no window any more, so this checks the thing the browser needs:
# the process comes up, loads its modules and prints the URL with a token.
check_serve_smoke() {
	local data_dir log
	data_dir=$(new_temp_dir)
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-serve.XXXXXX")
	temp_dirs+=("$log")
	set +e
	timeout -s TERM 30 "$GO" run ./cmd/zeokeel --product serve --no-tray --port 0 --data-dir "$data_dir" >"$log" 2>&1
	local code=$?
	set -e
	if [[ $code -ne 0 && $code -ne 124 ]]; then
		tail -n 40 "$log"
		return 1
	fi
	if grep -qiE 'panic|runtime error|SIGSEGV' "$log"; then
		tail -n 40 "$log"
		return 1
	fi
	if ! grep -q 'product: serve' "$log" || ! grep -q 'gateway: http://127.0.0.1:' "$log"; then
		tail -n 40 "$log"
		return 1
	fi
	printf '(serve exit %s, url printed: %s) ' "$code" "$(grep -c 'gateway: http://127.0.0.1:' "$log" || true)"
}

check_headless_smoke() {
	local data_dir log
	data_dir=$(new_temp_dir)
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-headless.XXXXXX")
	temp_dirs+=("$log")
	set +e
	timeout 20 "$GO" run ./cmd/zeokeel --headless --data-dir "$data_dir" >"$log" 2>&1
	local code=$?
	set -e
	if [[ $code -ne 0 && $code -ne 124 ]]; then
		tail -n 40 "$log"
		return 1
	fi
	if grep -qiE 'panic|runtime error|SIGSEGV' "$log"; then
		tail -n 40 "$log"
		return 1
	fi
	if ! grep -q 'product "headless" loaded' "$log" || ! grep -q 'ready; waiting for SIGINT or SIGTERM' "$log"; then
		tail -n 40 "$log"
		return 1
	fi
	printf '(headless exit %s) ' "$code"
}

check_acp_smoke() {
	local data_dir out err
	data_dir=$(new_temp_dir)
	out=$(mktemp "${TMPDIR:-/tmp}/zeokeel-acp-out.XXXXXX")
	err=$(mktemp "${TMPDIR:-/tmp}/zeokeel-acp-err.XXXXXX")
	temp_dirs+=("$out" "$err")
	set +e
	printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}\n' |
		timeout 20 "$GO" run ./cmd/zeokeel --acp --data-dir "$data_dir" >"$out" 2>"$err"
	local code=$?
	set -e
	if [[ $code -ne 0 ]]; then
		cat "$out" "$err"
		return 1
	fi
	if ! grep -q '"jsonrpc":"2.0"' "$out" || ! grep -q '"protocolVersion":1' "$out"; then
		cat "$out" "$err"
		return 1
	fi
	printf '(ACP exit %s) ' "$code"
}

echo "verify: $ROOT"
echo "verify: go=$("$GO" version 2>&1) quick=$([[ $RACE -eq 0 && $SMOKE -eq 0 ]] && echo yes || echo no)"
echo

run_step "gofmt" check_gofmt
run_step "go build (internal, cmd)" "$GO" build ./internal/... ./cmd/...
run_step "go vet (internal, cmd)" "$GO" vet ./internal/... ./cmd/...
run_step_quiet "protocol consistency (contract.json vs dispatch)" check_protocol_consistency
run_step_quiet "go test (internal, cmd) -count=1" "$GO" test ./internal/... ./cmd/... -count=1
if [[ $RACE -eq 1 ]]; then
	run_step_quiet "go test (internal, cmd) -count=1 -race" "$GO" test ./internal/... ./cmd/... -count=1 -race
else
	echo "==> go test (internal, cmd) -count=1 -race ... SKIP (--quick)"
fi
if [[ $SMOKE -eq 1 ]]; then
	run_step "serve smoke (gateway starts without a display)" check_serve_smoke
	run_step "headless smoke" check_headless_smoke
	run_step "ACP smoke" check_acp_smoke
else
	echo "==> product smokes ... SKIP (--no-smoke/--quick)"
fi
