#!/usr/bin/env bash
# A4 交互 QA 一键核对（供 Lead 收尾时跑）。
#
#   bash docs/research/redesign-2026-11/A4-qa/check.sh              # 构建 + 必须绿集合 + WIP 观测 + QA 套件
#   bash docs/research/redesign-2026-11/A4-qa/check.sh --with-drive # 另跑 ui-drive 的 a4qa- 步骤与交互步骤子集
#   A4QA_OUT=.cache/a4qa-check bash .../check.sh
#
# 结论看两行：
#   LOCKED（behavior-locked，必须绿）：Esc / 焦点归还 / 双击新建 / 拖入 / 禁用原因 / 树选中
#   WIP（实现中的其余表面，允许红到收尾）
# 退出码：0 = LOCKED 全绿且套件 0 FAIL；1 = LOCKED 或套件有回归；2 = 构建失败。
# EXPECTED-FAIL 是 A4 已确认"当前缺失、实现中"的用例，不计失败；实现落地后显示 PASS(fixed)。
#
# 只写 .cache/ 与 docs/research/redesign-2026-11/A4-qa/ 下的输出，不改产品代码。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
OUT="${A4QA_OUT:-$ROOT/.cache/a4qa-check}"
SCENARIO="$ROOT/docs/research/redesign-2026-11/A4-qa/interaction-drive.txt"
RUNNER="$ROOT/docs/research/redesign-2026-11/A4-qa/testdata/a4qa"

export PATH=/usr/local/go/bin:$PATH
export GOPATH=/data/mygo/.cache/gopath
export GOCACHE=/data/mygo/.cache/go-build
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
mkdir -p /tmp/zeokeel-tmp "$OUT"

WITH_DRIVE=0
for arg in "$@"; do
	case "$arg" in
	--with-drive) WITH_DRIVE=1 ;;
	*) echo "unknown flag: $arg" >&2; exit 2 ;;
	esac
done

cd "$ROOT" || exit 2

locked_pass=0
locked_fail=0
declare -a locked_failed=()
declare -a wip_red=()

stage() { printf '\n=== %s\n' "$1"; }

# build_failed <log>：日志显示的是编译/链接失败（树在改动中），而不是断言失败。
build_failed() {
	grep -qE '^(# |.*\[build failed\])' "$1" 2>/dev/null
}

# run_set <pkg> <regex> <tag>：跑一组 -run，成功返回 0。
run_set() {
	local pkg="$1" regex="$2" tag="$3"
	local log="$OUT/$tag-$(echo "$pkg" | tr '/.' '__').log"
	if go test "./$pkg" -run "$regex" -count=1 >"$log" 2>&1; then
		return 0
	fi
	grep -E '^(--- FAIL|# )' "$log" | head -8 | sed 's/^/        /'
	return 1
}

# ---------------------------------------------------------------- 1. 构建
stage "1/5 构建（go build ./... + ui-drive）"
if ! go build ./... >"$OUT/build.log" 2>&1; then
	echo "  FAIL  build"
	echo "  ↳ 见 $OUT/build.log（改动中的中间态；先修构建）"
	printf '\n== A4 QA: BUILD FAILED — 后续阶段跳过\n'
	exit 2
fi
echo "  PASS  build"
if ! go build ./internal/devtools/ui-drive >"$OUT/build-drive.log" 2>&1; then
	echo "  FAIL  build:ui-drive（含 steps_a4qa.go）"
	printf '\n== A4 QA: BUILD FAILED — 后续阶段跳过\n'
	exit 2
fi
echo "  PASS  build:ui-drive"

# ------------------------------------------- 2. LOCKED：六类行为必须有保护网
stage "2/5 LOCKED（behavior-locked：Esc / 焦点归还 / 双击新建 / 拖入 / 禁用原因 / 树选中）"
locked_sets=(
	"internal/uiroot|TestEscape|TestPaletteEscape|TestInspectorDrawerEscape|TestSettingsSurfaceEscape|TestFileDrop|TestShortcuts"
	"internal/regression|TestEscE2E|TestWindowShortcuts"
	"internal/uikit|TestApprovalDialogEscape|TestApprovalDialogBackdrop|TestAskDialog|TestPlanReview|TestConfirmDanger|TestComposerEnter|TestComposerEscape"
	"internal/ui/session|TestEsc|TestFindBar|TestComposerEnter|TestComposerQueue|TestComposerFocus|TestComposerRefocus|TestDropped|TestDrop|TestAttachmentPicker"
	"internal/ui/approval|TestEsc"
	"internal/ui/rightpanel|TestFilesTree|TestWorkspaceFileRowMenu"
)
for entry in "${locked_sets[@]}"; do
	pkg="${entry%%|*}"
	regex="${entry#*|}"
	if run_set "$pkg" "$regex" "locked"; then
		locked_pass=$((locked_pass + 1))
		printf '  PASS  %s\n' "$pkg"
	else
		locked_fail=$((locked_fail + 1))
		locked_failed+=("$pkg")
		printf '  FAIL  %s\n' "$pkg"
	fi
done

# ------------------------------------------------- 3. WIP：其余表面（观测）
stage "3/5 WIP（实现中的表面：允许红到收尾，仅观测）"
wip_pkgs=(internal/uikit internal/ui/session internal/ui/approval internal/ui/interaction internal/ui/settings internal/ui/toasts internal/ui/rightpanel internal/ui/sidebar internal/ui/plugins internal/uiroot internal/ui/uimodtest)
for pkg in "${wip_pkgs[@]}"; do
	log="$OUT/wip-$(echo "$pkg" | tr '/.' '__').log"
	if go test "./$pkg" -count=1 >"$log" 2>&1; then
		printf '  ok    %s\n' "$pkg"
	elif build_failed "$log"; then
		wip_red+=("$pkg")
		printf '  BUILD-FAIL  %s（树在改动中）\n' "$pkg"
	else
		wip_red+=("$pkg")
		printf '  RED   %s（%s 个失败测试）\n' "$pkg" "$(grep -c '^--- FAIL' "$log")"
	fi
done

# ------------------------------------------------------- 4. 交互 QA 套件
stage "4/5 交互 QA 套件（$SCENARIO）"
suite_log="$OUT/suite.log"
go run "$RUNNER" --scenario "$SCENARIO" --out "$OUT/suite" >"$suite_log" 2>&1
suite_code=$?
if build_failed "$suite_log"; then
	echo "  BUILD-FAIL  套件没有跑起来（产品树在改动中）"
	tail -5 "$suite_log" | sed 's/^/        /'
	printf '\n== A4 QA: BUILD FAILED — 套件阶段无法执行\n'
	exit 2
fi
grep -E '^(PASS|FAIL|EXPECTED-FAIL|PASS\(fixed\))' "$suite_log" | sed 's/^/  /' | tail -40
suite_expected=$(grep -c '^EXPECTED-FAIL' "$suite_log")
suite_known=$(grep -c '^KNOWN-LIMIT' "$suite_log")
suite_fail=$(grep -c '^FAIL ' "$suite_log")
printf '  → %s\n' "$(grep '^== A4 interaction QA' "$suite_log" | tail -1 | sed 's/^== //')"
[ "$suite_code" -ne 0 ] && [ "$suite_fail" -eq 0 ] && suite_fail=1

# ------------------------------------------------------------ 5. ui-drive
drive_fail=0
if [ "$WITH_DRIVE" -eq 1 ]; then
	stage "5/5 ui-drive（a4qa- 步骤 + 交互步骤子集）"
	go run ./internal/devtools/ui-drive --only a4qa- --out "$OUT/drive-a4qa" >"$OUT/drive-a4qa.log" 2>&1
	drive_results="$OUT/drive-a4qa/results.json"
	# 与套件 KNOWN-LIMIT 对应的两步（全量装配里焦点请求不落地，见报告 §7.5）。
	a4qa_known_gaps=""
	if build_failed "$OUT/drive-a4qa.log"; then
		echo "  BUILD-FAIL  ui-drive 没有跑起来（产品树在改动中）"
		printf '\n== A4 QA: BUILD FAILED — ui-drive 阶段无法执行\n'
		exit 2
	fi
	if [ -f "$drive_results" ]; then
		drive_fail=0
		gap_hits=0
		for gap in $a4qa_known_gaps; do
			if grep -q "\"id\": \"$gap\".*\"status\": \"FAIL\"" "$drive_results" 2>/dev/null || \
				python3 - "$drive_results" "$gap" <<'PY2' 2>/dev/null
import json,sys
rows=json.load(open(sys.argv[1]))
sys.exit(0 if any(r['id']==sys.argv[2] and r['status']=='FAIL' for r in rows) else 1)
PY2
			then
				gap_hits=$((gap_hits + 1))
			fi
		done
		drive_fail=$(( $(grep -c '"status": "FAIL"' "$drive_results") - gap_hits ))
		python3 - "$drive_results" <<'PY' 2>/dev/null || true
import json,sys
rows=json.load(open(sys.argv[1]))
for r in rows:
    mark={'PASS':'PASS','FAIL':'FAIL','WARN':'WARN','BLOCKED':'BLOCK'}.get(r.get('status'),r.get('status'))
    detail=(r.get('details') or [''])[0]
    print(f"  {mark:<4} {r['id']:<34} {detail[:90]}")
PY
		printf '  → ui-drive a4qa-：FAIL %d（另 %d 步为已知缺口，不计）\n' "$drive_fail" "$gap_hits"
	fi
	go run ./internal/devtools/ui-drive --only \
		palette-,settings-close,settings-dirty,stream-stop,queue-popover,toast-action,approval-safe,overlay-,mention-,slash-,composer-,window-,panel- \
		--out "$OUT/drive" >"$OUT/drive.log" 2>&1
	# The legacy ui-drive surface steps (steps_overlays.go 等) are NOT part of this
	# suite's contract and are still written against the pre-redesign copy, so
	# their failures are reported but do not gate the verdict.
	subset_fail=$(grep -c '"status": "FAIL"' "$OUT/drive/results.json" 2>/dev/null || true)
	subset_fail=${subset_fail:-0}
	printf '  → ui-drive 交互子集（非本套件契约，仅观测）：FAIL %s\n' "$subset_fail"
	[ "$subset_fail" -gt 0 ] && grep -o '"id": "[a-z0-9-]*"' "$OUT/drive/results.json" >/dev/null 2>&1 && true
fi

# ---------------------------------------------------------------- 汇总
stage "汇总"
printf '  LOCKED: %d 组绿 / %d 组红\n' "$locked_pass" "$locked_fail"
if [ "$locked_fail" -gt 0 ]; then
	for item in "${locked_failed[@]}"; do printf '    - %s\n' "$item"; done
fi
if [ "${#wip_red[@]}" -gt 0 ]; then
	printf '  WIP   : %d 个包红（允许到收尾）：%s\n' "${#wip_red[@]}" "${wip_red[*]}"
else
	printf '  WIP   : 0 个包红\n'
fi
printf '  SUITE : FAIL %d / EXPECTED-FAIL %d / KNOWN-LIMIT %d\n' "$suite_fail" "$suite_expected" "$suite_known"
[ "$WITH_DRIVE" -eq 1 ] && printf '  DRIVE-SUBSET : FAIL %s（旧步骤，文案改造后未迁移；不计入结论）\n' "$subset_fail"
[ "$WITH_DRIVE" -eq 1 ] && printf '  DRIVE-a4qa : FAIL %d\n' "$drive_fail"
printf '  明细  : %s\n' "$OUT"

if [ "$locked_fail" -gt 0 ] || [ "$suite_fail" -gt 0 ] || [ "$drive_fail" -gt 0 ]; then
	printf '结论：RED（LOCKED/套件/drive 有回归）\n'
	exit 1
fi
if [ "${#wip_red[@]}" -gt 0 ] || [ "$suite_expected" -gt 0 ]; then
	printf '结论：GREEN-LOCKED（必须绿项通过；WIP/EXPECTED-FAIL 待收尾清零）\n'
	exit 0
fi
if [ "$suite_known" -gt 0 ]; then
	printf '结论：GREEN（%d 条已知限制，见 A4-QA-REPORT §7.5）\n' "$suite_known"
	exit 0
fi
printf '结论：GREEN（全部清空）\n'
exit 0