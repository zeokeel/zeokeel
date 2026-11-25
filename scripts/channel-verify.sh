#!/usr/bin/env bash
# Channel ingress verification: prove the signed inbound paths answer honestly
# against a real running product (real-device runs, see docs/plans/channel-verification.md).
#
# Usage:
#   scripts/channel-verify.sh webhook <baseURL> <sourceID> <secret> [--expect-fail]
#   scripts/channel-verify.sh dingtalk <baseURL> <secret> [--expect-fail]
#   scripts/channel-verify.sh feishu-challenge <baseURL>
#
# Exit 0 means the answer was the one the channel contract promises.
set -euo pipefail

mode="${1:?mode required: webhook|dingtalk|feishu-challenge}"
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
  dingtalk)
    secret="${3:?secret required}"
    expect="202"
    [[ "${4:-}" == "--expect-fail" ]] && expect="401"
    ts=$(date +%s)
    string_to_sign=$(printf '%s\n%s' "$ts" "$secret")
    sig=$(printf '%s' "$string_to_sign" | openssl dgst -sha256 -hmac "$secret" -binary | openssl base64 -A)
    body='{"msgtype":"text","msgId":"verify-1","conversationId":"c1","senderStaffId":"u1","text":{"content":"channel-verify"}}'
    # The platform sends `timestamp` + `sign`; the X-DingTalk-* spelling is an alias
    # the adapter also accepts. Send the documented pair, keep the alias visible.
    code=$(curl -s -o /tmp/channel-verify.out -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" -H "timestamp: $ts" -H "sign: $sig" \
      --data-binary "$body" "$base/api/inbound/dingtalk")
    ;;
  feishu-challenge)
    expect="200"
    code=$(curl -s -o /tmp/channel-verify.out -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" --data-binary \
      '{"type":"url_verification","challenge":"verify-challenge"}' "$base/api/inbound/feishu")
    grep -q 'verify-challenge' /tmp/channel-verify.out || { echo "challenge not echoed"; cat /tmp/channel-verify.out; exit 1; }
    ;;
  *)
    echo "unknown mode: $mode" >&2; exit 2;;
esac

echo "HTTP $code (want $expect): $(head -c 200 /tmp/channel-verify.out)"
[[ "$code" == "$expect" ]]
