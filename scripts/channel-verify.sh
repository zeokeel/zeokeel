#!/usr/bin/env bash
# Channel ingress verification: prove the signed inbound path answers honestly
# against a real running product (real-device runs, see docs/plans/channel-verification.md).
#
# Usage:
#   scripts/channel-verify.sh webhook <baseURL> <sourceID> <secret> [--expect-fail]
#
# There is one ingress shape: the generic `webhook` source. DingTalk and Feishu
# are outbound only, so the `dingtalk` and `feishu-challenge` modes are gone with
# the ingress itself (docs/research/channel-capabilities.md §6).
#
# Exit 0 means the answer was the one the channel contract promises.
set -euo pipefail

mode="${1:?mode required: webhook}"
base="${2:?baseURL required, e.g. http://127.0.0.1:4141}"

case "$mode" in
  webhook)
    source="${3:?source id required}"
    secret="${4:?secret required}"
    expect="202"
    [[ "${5:-}" == "--expect-fail" ]] && expect="401"
    body='{"title":"channel-verify","body":"from scripts/channel-verify.sh","ref":"verify"}'
    ts=$(date +%s)
    sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$secret" -hex | sed 's/^.*= //')
    code=$(curl -s -o /tmp/channel-verify.out -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" -H "X-Zeokeel-Signature: t=$ts,v1=$sig" \
      --data-binary "$body" "$base/api/inbound/$source")
    ;;
  *)
    echo "unknown mode: $mode (only 'webhook' exists: DingTalk/Feishu are outbound only)" >&2; exit 2;;
esac

echo "HTTP $code (want $expect): $(head -c 200 /tmp/channel-verify.out)"
[[ "$code" == "$expect" ]]
