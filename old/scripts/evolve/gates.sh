#!/usr/bin/env bash
# ZeoKeel 自进化闭环 v0：统一门禁入口（Signal collector）。
#
# 用法:
#   scripts/evolve/gates.sh [--quick|--full]
#
# --quick（默认）: gofmt / vet / build / go test
# --full        : 额外跑 scripts/check-nocgo.sh（零 cgo + 四目标交叉编译）
#
# 行为:
#   - 自动补齐 /usr/local/go/bin 到 PATH（非登录 shell 里 make check 会 127）
#   - 测试类步骤失败自动重跑（默认最多 3 次）只用于暴露 flaky；重跑才通过记为
#     flaky 并按失败处理（exit 1）——不允许把"重试后绿"当成绿（AR-02）
#   - 2026-11-05: DSH 像素门禁步骤已退役（见 docs/decisions/0001-retire-dsh-pixel-parity.md）
#   - 输出: .evolve/state/latest.json（机器可读）、.evolve/state/latest.md（人读）、
#           .evolve/state/logs/<run-id>-<step>.log（原始日志）
#   - 任一确定性步骤失败 -> exit 1；仅 flaky -> exit 1 且报告 flaky 步骤
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
[[ -d /usr/local/go/bin ]] && PATH="/usr/local/go/bin:$PATH"
export PATH CGO_ENABLED=0

MODE=quick
for arg in "$@"; do
	case "$arg" in
	--quick) MODE=quick ;;
	--full) MODE=full ;;
	-h | --help)
		sed -n '2,16p' "$0"
		exit 0
		;;
	*)
		echo "gates: unknown argument: $arg (use --quick|--full)" >&2
		exit 2
		;;
	esac
done

command -v go >/dev/null 2>&1 || {
	echo "gates: go not found (PATH=$PATH)" >&2
	exit 2
}

RUN_ID=$(date +%Y%m%d-%H%M%S)
STATE="$ROOT/.evolve/state"
LOGS="$STATE/logs"
mkdir -p "$LOGS"
TSV=$(mktemp)
trap 'rm -f "$TSV"' EXIT

STARTED=$(date -Iseconds)
FAILED=0
FLAKY=0

# run_step <name> <attempts> <timeout_s> <cmd...>
run_step() {
	local name=$1 attempts=$2 tmo=$3
	shift 3
	local log="$LOGS/$RUN_ID-$name.log"
	local rc=1 attempt=1 begin end dur tail
	: >"$log"
	while ((attempt <= attempts)); do
		begin=$(date +%s%N)
		if command -v timeout >/dev/null 2>&1; then
			timeout "$tmo" "$@" >"$log" 2>&1
		else
			"$@" >"$log" 2>&1
		fi
		rc=$?
		end=$(date +%s%N)
		dur=$(((end - begin) / 1000000))
		printf '\n--- attempt %d/%d rc=%d (%dms) ---\n' "$attempt" "$attempts" "$rc" "$dur" >>"$log"
		((rc == 0)) && break
		attempt=$((attempt + 1))
		# 非测试类步骤没有重跑意义，由调用方传 attempts=1。
	done
	dur=$((dur / 1000))
	tail=$(tail -c 500 "$log" 2>/dev/null | tr '\n\t' '  ' | tr -s ' ' | cut -c1-400)
	local status=pass
	if ((rc != 0)); then
		status=fail
		FAILED=$((FAILED + 1))
	elif ((attempt > 1)); then
		# Retrying until green is how flaky tests get masked: report the step
		# as flaky AND count it as a gate failure.
		status=flaky
		FLAKY=$((FLAKY + 1))
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$name" "$status" "$attempt" "$dur" "$rc" "$tail" >>"$TSV"
	printf '  %-14s %-6s attempts=%d %4ds\n' "$name" "$status" "$attempt" "$dur"
}

echo "== ZeoKeel gates ($MODE) run $RUN_ID =="
run_step fmt 1 60 bash -c 'out=$(gofmt -l ./cmd ./internal); if [ -n "$out" ]; then echo "$out"; exit 1; fi'
run_step vet 1 180 go vet ./...
run_step build 1 180 go build ./...
run_step test 3 900 go test -count=1 ./...

if [[ $MODE == full ]]; then
	run_step nocgo 1 600 sh scripts/check-nocgo.sh
fi

FINISHED=$(date -Iseconds)
GOVER=$(go version 2>/dev/null | tr '\n' ' ')
export RUN_ID MODE STARTED FINISHED GOVER ROOT FAILED FLAKY
python3 - "$TSV" "$STATE" <<'PY'
import json, os, sys

tsv, state = sys.argv[1], sys.argv[2]

env = os.environ
steps = []
with open(tsv) as f:
    for line in f:
        line = line.rstrip("\n")
        if not line:
            continue
        name, status, attempts, dur, rc, tail = line.split("\t", 5)
        log = os.path.join(state, "logs", f"{env['RUN_ID']}-{name}.log")
        step = {
            "name": name, "status": status,
            "attempts": int(attempts), "duration_s": int(dur),
            "exit_code": int(rc), "tail": tail, "log": log,
        }
        steps.append(step)
run = {
    "schema": "zeokeel.evolve.gates/v1",
    "run_id": env["RUN_ID"], "mode": env["MODE"],
    "root": env["ROOT"], "go_version": env["GOVER"],
    "started_at": env["STARTED"], "finished_at": env["FINISHED"],
    "ok": int(env["FAILED"]) == 0 and int(env["FLAKY"]) == 0,
    "failed_steps": [s["name"] for s in steps if s["status"] == "fail"],
    "flaky_steps": [s["name"] for s in steps if s["status"] == "flaky"],
    "flaky_count": int(env["FLAKY"]),
    "steps": steps,
}
with open(os.path.join(state, "latest.json"), "w") as f:
    json.dump(run, f, ensure_ascii=False, indent=2)
    f.write("\n")

md = ["# Gates run " + run["run_id"], "",
      f"- mode: `{run['mode']}`  ok: **{run['ok']}**",
      f"- go: {run['go_version'].strip()}  started: {run['started_at']}  finished: {run['finished_at']}",
      "", "| step | status | attempts | duration |", "| --- | --- | ---: | ---: |"]
for s in steps:
    md.append(f"| {s['name']} | {s['status']} | {s['attempts']} | {s['duration_s']}s |")
if run["flaky_steps"]:
    md += ["", "> flaky（重跑才通过，按失败处理）: " + ", ".join(run["flaky_steps"]),
           "> 注意：flaky 不是「通过」；本次 run 的 ok 为 false。"]
if run["failed_steps"]:
    md += ["", "> 失败: " + ", ".join(run["failed_steps"])]
md += ["", "日志: `.evolve/state/logs/`；机器可读: `.evolve/state/latest.json`"]
with open(os.path.join(state, "latest.md"), "w") as f:
    f.write("\n".join(md) + "\n")
print("\nwrote .evolve/state/latest.json and latest.md")
PY
rm -f "$TSV"

echo
if ((FAILED > 0)); then
	echo "GATES FAIL ($FAILED deterministic failure[s]) — see .evolve/state/latest.md" >&2
	exit 1
fi
if ((FLAKY > 0)); then
	echo "GATES FLAKY ($FLAKY step[s] passed only on retry; treated as failure) — see .evolve/state/latest.md" >&2
	exit 1
fi
echo "GATES OK — see .evolve/state/latest.md"
