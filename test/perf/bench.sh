#!/usr/bin/env bash
# Boot a UWAS binary against the perf config, drive it with loadgen, print JSON.
#
#   test/perf/bench.sh                                  # defaults
#   test/perf/bench.sh -b bin/uwas-base -H cached.perf  # a different binary/vhost
#   test/perf/bench.sh -r 30000                         # open loop at 30k req/s
#
# Run from the repository root.
set -euo pipefail

BIN=bin/uwas
LOADGEN=bin/loadgen
CONFIG=test/perf/uwas-perf.yaml
HOST=static.perf
PATH_=/index.html
CONNS=50
DURATION=10s
WARMUP=3s
RATE=0
ENC=""
LABEL=""
PORT=19180

usage() { sed -n '2,9p' "$0"; exit "${1:-0}"; }

while getopts "b:c:d:w:H:p:r:e:l:P:h" opt; do
  case "$opt" in
    b) BIN=$OPTARG ;;
    c) CONNS=$OPTARG ;;
    d) DURATION=$OPTARG ;;
    w) WARMUP=$OPTARG ;;
    H) HOST=$OPTARG ;;
    p) PATH_=$OPTARG ;;
    r) RATE=$OPTARG ;;
    e) ENC=$OPTARG ;;
    l) LABEL=$OPTARG ;;
    P) PORT=$OPTARG ;;
    h) usage 0 ;;
    *) usage 1 ;;
  esac
done

[ -x "$BIN" ] || { echo "bench.sh: no binary at $BIN (run: go build -o $BIN ./cmd/uwas)" >&2; exit 1; }
[ -x "$LOADGEN" ] || go build -o "$LOADGEN" ./test/perf/loadgen
[ -n "$LABEL" ] || LABEL="$(basename "$BIN")/$HOST"

LOG=$(mktemp -t uwas-perf-XXXXXX)
"$BIN" serve -c "$CONFIG" >"$LOG" 2>&1 &
SRV=$!
# Kill the server whatever happens, including a failed readiness wait.
trap 'kill "$SRV" 2>/dev/null || true; wait "$SRV" 2>/dev/null || true' EXIT

for _ in $(seq 100); do
  if curl -fsS -o /dev/null -H "Host: $HOST" "http://127.0.0.1:$PORT$PATH_" 2>/dev/null; then
    ready=1; break
  fi
  sleep 0.1
done
if [ "${ready:-0}" != 1 ]; then
  echo "bench.sh: server never became ready on port $PORT; log follows" >&2
  cat "$LOG" >&2
  exit 1
fi

ARGS=(-url "http://127.0.0.1:$PORT$PATH_" -host "$HOST"
      -c "$CONNS" -d "$DURATION" -warmup "$WARMUP" -rate "$RATE" -label "$LABEL")
[ -n "$ENC" ] && ARGS+=(-accept-encoding "$ENC")

"$LOADGEN" "${ARGS[@]}"
