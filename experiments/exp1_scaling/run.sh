#!/usr/bin/env bash
# Horizontal scaling: same load with 1, 2, ... gateway replicas.
# Budgets are huge so the limiter never rejects, but every request still goes
# through Redis, so Redis load grows with traffic.
#   REPLICAS="1 2 4" USERS=100 DURATION=90s bash experiments/exp1_scaling/run.sh
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
RESULTS="$ROOT/experiments/exp1_scaling/results"
mkdir -p "$RESULTS"

export LIMITER=redis POLICY=least_tokens QUOTAS_FILE=configs/quotas-loadtest.yaml

for n in ${REPLICAS:-1 2}; do
  echo "=== $n gateway replica(s) ==="
  export GATEWAYS="$n"
  start_system
  run_locust "$RESULTS/gw${n}"
  stop_system
done

python3 "$ROOT/experiments/summarize.py" "$RESULTS"/gw*_stats.csv
