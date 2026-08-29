#!/usr/bin/env bash
# Interleaved A/B comparison of two UWAS binaries.
#
#   test/perf/ab.sh bin/uwas-base bin/uwas
#   test/perf/ab.sh -n 7 -r 30000 bin/uwas-base bin/uwas
#
# Rounds alternate A,B,A,B,... and the reported number is the median of each
# side's rounds. Interleaving matters: a laptop's clocks drift, other
# processes come and go, and running all of A then all of B hands that drift
# straight to the winner. Alternating spreads it evenly, and the median
# throws away the round where Spotlight woke up.
#
# Anything under ~3% apart is noise on a shared machine — treat it as a tie.
set -euo pipefail

ROUNDS=5
EXTRA=()
while getopts "n:c:d:H:p:r:e:h" opt; do
  case "$opt" in
    n) ROUNDS=$OPTARG ;;
    c|d|H|p|r|e) EXTRA+=("-$opt" "$OPTARG") ;;
    h) sed -n '2,14p' "$0"; exit 0 ;;
    *) sed -n '2,14p' "$0"; exit 1 ;;
  esac
done
shift $((OPTIND - 1))

[ $# -eq 2 ] || { echo "usage: ab.sh [-n rounds] [bench.sh flags] BIN_A BIN_B" >&2; exit 1; }
A=$1; B=$2
HERE=$(dirname "$0")
OUT=$(mktemp -d -t uwas-ab-XXXXXX)

for i in $(seq "$ROUNDS"); do
  echo "round $i/$ROUNDS: A" >&2
  "$HERE/bench.sh" -b "$A" -l "A" "${EXTRA[@]}" > "$OUT/a-$i.json"
  echo "round $i/$ROUNDS: B" >&2
  "$HERE/bench.sh" -b "$B" -l "B" "${EXTRA[@]}" > "$OUT/b-$i.json"
done

A_LABEL=$A B_LABEL=$B python3 - "$OUT" <<'PY'
import glob, json, os, statistics, sys

d = sys.argv[1]
def load(p):
    runs = [json.load(open(f)) for f in sorted(glob.glob(os.path.join(d, p)))]
    keys = ["rps", "p50", "p90", "p99", "p999"]
    out = {}
    for k in keys:
        vals = [r["rps"] if k == "rps" else r["latency_ms"][k] for r in runs]
        out[k] = statistics.median(vals)
    out["errors"] = sum(r["errors"] for r in runs)
    return out

a, b = load("a-*.json"), load("b-*.json")
print(f"\nA = {os.environ['A_LABEL']}\nB = {os.environ['B_LABEL']}   (median of rounds)\n")
print(f"{'metric':>8}  {'A':>12}  {'B':>12}  {'delta':>9}")
print("-" * 48)
for k in ["rps", "p50", "p90", "p99", "p999"]:
    # More requests per second is better; less latency is better.
    delta = (b[k] - a[k]) / a[k] * 100 if a[k] else 0.0
    better = delta > 0 if k == "rps" else delta < 0
    mark = "  <-- B wins" if abs(delta) >= 3 and better else ("  <-- A wins" if abs(delta) >= 3 else "  (tie)")
    unit = "" if k == "rps" else "ms"
    print(f"{k:>8}  {a[k]:>10.3f}{unit:2}  {b[k]:>10.3f}{unit:2}  {delta:>+8.1f}%{mark}")
if a["errors"] or b["errors"]:
    print(f"\nerrors: A={a['errors']} B={b['errors']} — investigate before trusting the numbers")
PY
