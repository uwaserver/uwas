#!/usr/bin/env bash
# Run the local autoblock / rate-limit lab.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
make -s dev
go build -o /tmp/autoblock-lab ./test/autoblock-lab
exec /tmp/autoblock-lab "$@"
