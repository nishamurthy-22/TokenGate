#!/usr/bin/env bash
# Routing policies on one fast and one slow backend, same load each time.
# The limiter is off so only routing affects the results.
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
RESULTS="$ROOT/experiments/exp2_routing/results"
mkdir -p "$RESULTS"

export LIMITER=none GATEWAYS="${GATEWAYS:-2}"

for policy in ${POLICIES:-round_robin least_requests least_tokens}; do
  echo "=== policy $policy ==="
  export POLICY="$policy"
  start_system
  run_locust "$RESULTS/${policy}"
  stop_system
done

python3 "$ROOT/experiments/summarize.py" "$RESULTS"/*_stats.csv
