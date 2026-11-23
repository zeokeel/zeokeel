#!/usr/bin/env bash
# Acceptance fixture: the real gateway + a deterministic OpenAI-compatible model.
#
#   scripts/acceptance/serve-with-fake-model.sh [--model NAME] [--port PORT] [--with-tool]
#
# Starts `fakemodel` (internal/devtools/fakemodel), points the product at it
# through the ordinary settings rows (chat.baseURL / chat.model / chat.apiKey,
# written into the data directory's settings.json — the same file the settings
# screen writes), then starts `zeokeel --product serve` and prints the URL with
# its token. Ctrl-C stops both.
#
# The model name selects the script, so no product code knows this fixture exists:
#   acceptance-plain  a short streamed answer
#   acceptance-tool   a tool call (bash) that parks on the approval card, then a summary
#   acceptance-slow   one token every 200 ms for ~30 s (stop / disconnect testing)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

model="acceptance-plain"
port=""
probe=""
data_dir="${ACCEPTANCE_DATA_DIR:-$repo_root/.cache/acceptance/data}"
model_port="${ACCEPTANCE_MODEL_PORT:-9977}"

while [ $# -gt 0 ]; do
	case "$1" in
	--model) model="$2"; shift 2 ;;
	--plain) model="acceptance-plain"; shift ;;
	--slow) model="acceptance-slow"; shift ;;
	--port) port="$2"; shift 2 ;;
	--with-tool) model="acceptance-tool"; shift ;;
	--probe)
		# Run the Go probe against the fixture and exit with its verdict: the
		# CI path (no browser, no Playwright).
		probe="$2"
		shift 2
		;;
	--help|-h)
		sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

export PATH="/usr/local/go/bin:$PATH"
export GOPATH="${GOPATH:-$repo_root/.cache/gopath}"
export GOCACHE="${GOCACHE:-$repo_root/.cache/go-build}"
export GOTMPDIR="${GOTMPDIR:-/tmp/zeokeel-tmp}" TMPDIR="${TMPDIR:-/tmp/zeokeel-tmp}"
export GOFLAGS="${GOFLAGS:--mod=mod}" GOPROXY="${GOPROXY:-off}" GOSUMDB="${GOSUMDB:-off}"
mkdir -p "$GOTMPDIR" "$data_dir" "$repo_root/.cache/bin"

echo "== building the fixture binaries"
go build -o "$repo_root/.cache/bin/fakemodel" ./internal/devtools/fakemodel
go build -o "$repo_root/.cache/bin/zeokeel-acceptance" ./cmd/zeokeel

# The settings file is the product's own store: writing it before startup is the
# same path the settings screen takes, not a special "test mode".
python3 - "$data_dir/settings.json" "$model" "$model_port" <<'PY'
import json, sys
path, model, port = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    with open(path) as handle:
        rows = json.load(handle)
except (FileNotFoundError, json.JSONDecodeError):
    rows = {}
rows.update({
    "chat.baseURL": f"http://127.0.0.1:{port}/v1",
    "chat.model": model,
    "chat.apiKey": "acceptance-fake-key",
    "agent.permission": "workspace",
})
with open(path, "w") as handle:
    json.dump(rows, handle, indent=2, sort_keys=True)
print(f"== settings written: {path} (model={model})")
PY

cleanup() {
	local code=$?
	trap - INT TERM EXIT
	if [ -n "${serve_pid:-}" ]; then kill "$serve_pid" 2>/dev/null || true; wait "$serve_pid" 2>/dev/null || true; fi
	if [ -n "${model_pid:-}" ]; then kill "$model_pid" 2>/dev/null || true; wait "$model_pid" 2>/dev/null || true; fi
	exit $code
}
trap cleanup INT TERM EXIT

echo "== starting the fake model on 127.0.0.1:$model_port"
"$repo_root/.cache/bin/fakemodel" -addr "127.0.0.1:$model_port" &
model_pid=$!
for _ in $(seq 1 50); do
	if curl -fsS "http://127.0.0.1:$model_port/health" >/dev/null 2>&1; then break; fi
	sleep 0.1
done
if ! curl -fsS "http://127.0.0.1:$model_port/health" >/dev/null 2>&1; then
	echo "!! the fake model did not start" >&2
	exit 1
fi

echo "== starting the gateway (serve)"
serve_args=(--product serve --data-dir "$data_dir")
if [ -n "$port" ]; then serve_args+=(--port "$port"); fi
"$repo_root/.cache/bin/zeokeel-acceptance" "${serve_args[@]}" &
serve_pid=$!

info="$data_dir/gateway.json"
for _ in $(seq 1 100); do
	if [ -s "$info" ]; then break; fi
	if ! kill -0 "$serve_pid" 2>/dev/null; then
		echo "!! the gateway exited during startup" >&2
		exit 1
	fi
	sleep 0.1
done

# gateway.json deliberately stores no URL (the token is printed once, §3.1), so
# the URL is composed from the port and the token.
url="$(python3 - "$info" <<'PY'
import json, sys
try:
    with open(sys.argv[1]) as handle:
        info = json.load(handle)
    print(f"http://127.0.0.1:{info['port']}/#token={info['token']}")
except Exception:
    pass
PY
)"
if [ -z "$url" ]; then
	echo "!! $info has no port/token yet; see the gateway log above" >&2
	exit 1
fi

if [ -n "$probe" ]; then
	echo "== running gwprobe -mode $probe"
	if "$repo_root/.cache/bin/gwprobe" -addr "127.0.0.1:$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["port"])' "$info")" \
		-token "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$info")" \
		-mode "$probe"; then
		echo "== probe $probe PASSED"
		exit 0
	fi
	echo "!! probe $probe FAILED" >&2
	exit 1
fi

cat <<EOF

==================================================================
 ZeoKeel acceptance fixture is up.

   browser URL : $url

   model       : $model  (scenario: ${model#acceptance-})
   fake model  : http://127.0.0.1:$model_port/v1/chat/completions
   data dir    : $data_dir
   gateway log : stdout of this script

 Try in the browser: new session → send a turn → (tool: approve the card)
 → watch the answer stream in → reload the page and confirm it is still there.
 Ctrl-C stops the gateway and the fake model together.
==================================================================

EOF

wait "$serve_pid"