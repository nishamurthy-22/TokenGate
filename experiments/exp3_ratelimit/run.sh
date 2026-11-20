#!/usr/bin/env bash
# Rate-limit correctness: how far the naive (non-atomic) limiter and the atomic
# Redis limiter overshoot the quota when many clients reserve at once.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
RESULTS="$ROOT/experiments/exp3_ratelimit/results"
mkdir -p "$RESULTS"
OUT="$RESULTS/overshoot.csv"

compose up -d redis
sleep 2

header=true
: > "$OUT"
for w in ${WORKERS:-1 10 50 100}; do
  echo "=== $w concurrent clients ==="
  (cd "$ROOT" && go run ./cmd/rlbench -redis localhost:6379 -workers "$w" -runs "${RUNS:-3}" \
    -limiters naive,redis -header="$header") | tee -a "$OUT"
  header=false
done

compose stop redis
echo "Results written to $OUT"
