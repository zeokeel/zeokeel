#!/usr/bin/env bash
# One-command verification for ZeoKeel.
#
# Runs the checks from docs/VERIFICATION.md in order and prints a PASS/FAIL line
# with the duration of every step; any failure exits non-zero (the EXIT trap
# still prints the summary). Everything runs offline and caches through the
# local Go module cache.
#
# Usage:
#   scripts/verify.sh            # full: fmt/build/vet/test/-race/regression/snapshot/smokes
#   scripts/verify.sh --quick    # skip -race and the product smokes (fast inner loop)
#   scripts/verify.sh --no-smoke # keep -race, skip only the product smokes
#
# Environment (all overridable):
#   GO=/usr/local/go/bin/go  GOPROXY=off  GOSUMDB=off  GOFLAGS=-mod=mod
# Extra: VERIFY_KEEP_TEMP=1 keeps the temporary snapshot/data directories.
set -euo pipefail

# Offline toolchain defaults; the caller may override them.
case ":$PATH:" in
*:/usr/local/go/bin:*) ;;
*) export PATH="/usr/local/go/bin:$PATH" ;;
esac
export GO="${GO:-go}"
export GOFMT="${GOFMT:-gofmt}"
export GOPROXY="${GOPROXY:-off}"
export GOSUMDB="${GOSUMDB:-off}"
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
	unformatted=$("$GO" list -f '{{.Dir}}' ./... | sort -u | while IFS= read -r dir; do
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

check_regression() {
	local log
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-regression.XXXXXX")
	temp_dirs+=("$log")
	if ! "$GO" test ./internal/regression -count=1 -v >"$log" 2>&1; then
		tail -n 40 "$log"
		return 1
	fi
	local pass_count skip_count fail_count
	pass_count=$(grep -cE '^--- PASS' "$log" || true)
	skip_count=$(grep -cE '^--- SKIP' "$log" || true)
	fail_count=$(grep -cE '^--- FAIL' "$log" || true)
	printf '(regression: %s PASS / %s SKIP / %s FAIL) ' "$pass_count" "$skip_count" "$fail_count"
	[[ "$fail_count" -eq 0 ]]
}

check_snapshot() {
	local out_dir log
	out_dir=$(new_temp_dir)
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-snapshot.XXXXXX")
	temp_dirs+=("$log")
	if ! "$GO" run ./internal/devtools/ui-snapshot "$out_dir" >"$log" 2>&1; then
		tail -n 40 "$log"
		return 1
	fi
	local steps pngs
	steps=$(grep -c '^wrote ' "$log" || true)
	pngs=$(find "$out_dir" -type f -name '*.png' | wc -l | tr -d ' ')
	printf '(snapshot: %s steps / %s PNGs) ' "$steps" "$pngs"
	[[ "$steps" -gt 0 && "$pngs" -gt 0 ]]
}

check_desktop_smoke() {
	local data_dir log
	data_dir=$(new_temp_dir)
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-desktop.XXXXXX")
	temp_dirs+=("$log")
	if ! command -v xvfb-run >/dev/null 2>&1; then
		echo "xvfb-run is not installed (needed for the desktop smoke)"
		return 1
	fi
	cat >"$data_dir/settings.json" <<'JSON'
{"ui.globalHotkey":"CmdOrCtrl+Shift+Space"}
JSON
	set +e
	xvfb-run -a timeout -s TERM 8 "$GO" run ./cmd/zeokeel --data-dir "$data_dir" >"$log" 2>&1
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
	if ! grep -q 'product "desktop" loaded' "$log"; then
		tail -n 40 "$log"
		return 1
	fi
	printf '(desktop exit %s, tray ready: %s) ' "$code" \
		"$(grep -c 'system tray ready' "$log" || true)"
}

check_headless_smoke() {
	local data_dir log
	data_dir=$(new_temp_dir)
	log=$(mktemp "${TMPDIR:-/tmp}/zeokeel-headless.XXXXXX")
	temp_dirs+=("$log")
	set +e
	timeout 8 "$GO" run ./cmd/zeokeel --headless --data-dir "$data_dir" >"$log" 2>&1
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
		timeout 10 "$GO" run ./cmd/zeokeel --acp --data-dir "$data_dir" >"$out" 2>"$err"
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
run_step "go build ./..." "$GO" build ./...
run_step "go vet ./..." "$GO" vet ./...
run_step_quiet "go test ./... -count=1" "$GO" test ./... -count=1
if [[ $RACE -eq 1 ]]; then
	run_step_quiet "go test ./... -count=1 -race" "$GO" test ./... -count=1 -race
else
	echo "==> go test ./... -count=1 -race ... SKIP (--quick)"
fi
run_step "regression (-v)" check_regression
run_step "ui-snapshot" check_snapshot
if [[ $SMOKE -eq 1 ]]; then
	run_step "desktop smoke (xvfb)" check_desktop_smoke
	run_step "headless smoke" check_headless_smoke
	run_step "ACP smoke" check_acp_smoke
else
	echo "==> product smokes ... SKIP (--no-smoke/--quick)"
fi
