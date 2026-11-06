#!/bin/sh
# Real-model E2E: one short turn through the real provider, Controller and
# Agent. The key is read from DEEPSEEK_API_KEY or the DSH credentials file and
# is never echoed. Exits non-zero when the test fails or no key is available.
set -eu

cd "$(dirname "$0")/.."

if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
	creds="${ZEOKEEL_CREDENTIALS_FILE:-/root/.dsh/.credentials.yaml}"
	if [ -r "$creds" ]; then
		DEEPSEEK_API_KEY=$(awk '
			/^[[:space:]]*DEEPSEEK_API_KEY:[[:space:]]*/ {
				sub(/^[^:]*:[[:space:]]*/, "")
				gsub(/["\047]/, "")
				sub(/[[:space:]]+$/, "")
				print
				exit
			}
		' "$creds")
	fi
	if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
		echo "e2e-model: no DEEPSEEK_API_KEY in the environment or $creds" >&2
		exit 1
	fi
fi

export DEEPSEEK_API_KEY

exec go test -tags e2e -run TestRealModel -count=1 -v ./internal/e2e
