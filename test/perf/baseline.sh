#!/usr/bin/env bash
# Build a UWAS binary from a git ref, for use as the A side of ab.sh.
#
#   test/perf/baseline.sh HEAD          -> bin/uwas-base
#   test/perf/baseline.sh v0.9.1 bin/uwas-091
#
# Uses a throwaway worktree so your working tree — which holds the change
# you are trying to measure — is never touched.
set -euo pipefail

REF=${1:-HEAD}
OUT=${2:-bin/uwas-base}
WT=$(mktemp -d -t uwas-base-XXXXXX)

git worktree add --detach "$WT" "$REF" >/dev/null
trap 'git worktree remove --force "$WT" >/dev/null 2>&1 || true' EXIT

( cd "$WT" && go build -o "$OLDPWD/$OUT" ./cmd/uwas )
echo "built $OUT from $REF ($(git rev-parse --short "$REF"))"
