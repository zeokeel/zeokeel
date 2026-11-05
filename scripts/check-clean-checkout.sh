#!/bin/sh
# Clean-checkout build guard (BUILD-01 / AR-02).
#
# Catches a release blocker that a working-tree build cannot see: a *tracked*
# source file that is also matched by .gitignore, so it never reaches the commit
# and a clean checkout cannot build. That happened in wave 1
# (internal/importer/credentials.go matched the `credentials*` rule).
#
# `git archive HEAD` exports exactly the tracked content of the commit (no
# ignored files, no untracked files, no .git), which is what a fresh CI checkout
# contains; the build then runs in a temp directory.
#
# Usage: sh scripts/check-clean-checkout.sh
# Exit: 0 the committed tree builds, 1 it does not, 0 with a skip when not in a
# git checkout (source tarball builds).
set -eu
cd "$(dirname "$0")/.."

export PATH="/usr/local/go/bin:$PATH"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

if ! git rev-parse --git-dir >/dev/null 2>&1; then
	echo "clean-checkout: skipped (not a git checkout)"
	exit 0
fi

commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/zeokeel-clean-checkout.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# Fail loudly if the archive step itself breaks: a silently empty tree would
# make the build guard pass for the wrong reason.
git archive HEAD | tar -x -C "$tmp"
if [ ! -f "$tmp/go.mod" ] || [ ! -d "$tmp/cmd" ]; then
	echo "clean-checkout: FAIL: git archive HEAD did not produce a Go tree" >&2
	exit 1
fi

if ! ( cd "$tmp" && go build ./... ); then
	echo "clean-checkout: FAIL: HEAD ($commit) does not build from tracked files only." >&2
	echo "  Hint: check 'git check-ignore -v <file>' for tracked files matched by .gitignore." >&2
	exit 1
fi
echo "clean-checkout: PASS (HEAD $commit builds from tracked files only)"
