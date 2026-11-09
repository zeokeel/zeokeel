#!/usr/bin/env bash
# Phase 0 design-contract checks: the grep probes from docs/DESIGN-LINT.md
# appendix B, with count thresholds taken from the current baseline.
#
# These greps are the cheap, dependency-free safety net around designlint
# (internal/devtools/designlint): they catch the classic violations even if a
# contributor forgets to run the AST tool, and they run in about a second. The
# thresholds are upper bounds: migrations only lower them. Override them with
# the environment variables below when a change temporarily raises a count.
#
#   DS01_MAX                 color literals (ui.Hex/ui.RGBA) in UI packages
#   DS03_FONTSIZE_MAX        raw .FontSize(...) calls in UI packages
#   DS03_METRIC_MAX          raw numeric metric calls in UI packages
#   DS06_BUTTONBASE_MAX      self-drawn ui.ButtonBase(...) calls in internal/
#   DS10_DANGER_DEFAULT_MAX  DialogAction{Danger:true, Default:true} combos
#   DS09_STATUS_FUNCS_MAX    duplicate status->(label,color) functions
#
# Exit codes: 0 all checks pass, 1 at least one check is over its threshold.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DS01_MAX="${DS01_MAX:-0}"
DS03_FONTSIZE_MAX="${DS03_FONTSIZE_MAX:-0}"
DS03_METRIC_MAX="${DS03_METRIC_MAX:-51}"
DS06_BUTTONBASE_MAX="${DS06_BUTTONBASE_MAX:-10}"
DS10_DANGER_DEFAULT_MAX="${DS10_DANGER_DEFAULT_MAX:-0}"
DS09_STATUS_FUNCS_MAX="${DS09_STATUS_FUNCS_MAX:-0}"

# The lint implementation itself names the forbidden patterns in comments and
# messages; it is not product code and must not self-inflate the counts.
LINT_PATHS='^internal/(design/conform|devtools/designlint)/'

failed=0
passed=0

# count_lines prints the number of newline-separated matches.
count_lines() {
	if [[ -z "${1:-}" ]]; then
		printf '0'
		return 0
	fi
	printf '%s\n' "$1" | grep -c .
}

# run_check <name> <max> <match-lines> [<grep-command-for-display>]
run_check() {
	local name="$1" max="$2" lines="$3" count
	count="$(count_lines "$lines")"
	if [[ "$count" -gt "$max" ]]; then
		printf 'FAIL %-34s %3d > max %d\n' "$name" "$count" "$max"
		if [[ -n "$lines" ]]; then
			printf '%s\n' "$lines" | sed 's/^/    /'
		fi
		failed=$((failed + 1))
		return 0
	fi
	printf 'PASS %-34s %3d <= max %d\n' "$name" "$count" "$max"
	passed=$((passed + 1))
}

echo "design-check: $ROOT"

# DS-01 color literals: ui.Hex/ui.RGBA in the UI packages, tests excluded.
ds01_lines=$(grep -rnE 'ui\.(Hex|RGBA)\(' internal/ui internal/uiroot --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-01 color literals" "$DS01_MAX" "$ds01_lines"

# DS-03 raw .FontSize(...) calls (mono type role pending, DESIGN.md D6).
fontsize_lines=$(grep -rn '\.FontSize(' internal/ui internal/uiroot --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-03 raw FontSize" "$DS03_FONTSIZE_MAX" "$fontsize_lines"

# DS-03 raw numeric metrics (font size, spacing, radius, size, shadow).
metric_lines=$(grep -rnE '\.(Height|Width|Size|Gap|Padding|PaddingX|PaddingY|PaddingStart|PaddingEnd|MaxHeight|MinHeight|MinWidth|MaxWidth|Radius|FontSize|Shadow)\(-?[0-9]' \
	internal/ui internal/uiroot --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-03 raw metric literals" "$DS03_METRIC_MAX" "$metric_lines"

# DS-06 self-drawn ButtonBase call sites (all are registered in the registry
# allowlist today; new call sites need an entry, and uikit.IconButton
# convergence lowers this count).
buttonbase_lines=$(grep -rn 'ui\.ButtonBase(' internal --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-06 self-drawn ButtonBase" "$DS06_BUTTONBASE_MAX" "$buttonbase_lines"

# DS-10 Danger+Default in one DialogAction literal. This regex only sees the
# single-line form; designlint's AST rule is authoritative (it also catches the
# multi-line literal), this is the fast probe.
danger_default_lines=$(grep -rnE 'Danger:\s*true[^}]*Default:\s*true|Default:\s*true[^}]*Danger:\s*true' \
	internal --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-10 Danger+Default combo" "$DS10_DANGER_DEFAULT_MAX" "$danger_default_lines"

# DS-09 duplicate status->(label,color) functions (uikit.StatusText/Badge
# convergence lowers this count).
status_lines=$(grep -rnE '^func (statusLabel|normalizeStatus|statusColor|toolStatusColors|statusTintAlpha|toolStatusBadge|taskStatusLabel|taskStatusColor|subagentStatusLabel)' \
	internal --include='*.go' 2>/dev/null |
	grep -v '_test\.go:' | grep -vE "$LINT_PATHS" || true)
run_check "DS-09 duplicate status funcs" "$DS09_STATUS_FUNCS_MAX" "$status_lines"

echo "design-check: ${passed} passed, ${failed} failed"
if [[ "$failed" -ne 0 ]]; then
	echo "design-check: FAIL (probes: docs/DESIGN-LINT.md appendix B; contract: docs/DESIGN.md)"
	exit 1
fi
echo "design-check: PASS"
