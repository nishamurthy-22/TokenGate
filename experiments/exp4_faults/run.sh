#!/usr/bin/env bash
# Fault tolerance: the slow backend fails 30 s into a 90 s run and recovers
# 30 s later. Run twice:
#   baseline   no health checks, no retries
#   resilient  health checks and one retry
set -euo pipefail
source "$(dirname "$0")/../lib.sh"
RESULTS="$ROOT/experiments/exp4_faults/results"
mkdir -p "$RESULTS"

export LIMITER=none POLICY=round_robin GATEWAYS="${GATEWAYS:-2}" DURATION=90s

run_case() {
  local name="$1"
  echo "=== $name (health checks=$HEALTH_CHECKS, retries=$MAX_RETRIES) ==="
  start_system
  run_locust "$RESULTS/$name" --csv-full-history &
  local locust_pid=$!
  sleep 30
  echo "Injecting failure into backend-slow"
  curl -s -X POST "http://localhost:9002/admin/fail?mode=error" >/dev/null || true
  sleep 30
  echo "Recovering backend-slow"
  curl -s -X POST "http://localhost:9002/admin/recover" >/dev/null || true
  wait "$locust_pid" || true
  stop_system
}

HEALTH_CHECKS=false MAX_RETRIES=0 run_case baseline
HEALTH_CHECKS=true MAX_RETRIES=1 run_case resilient

python3 "$ROOT/experiments/summarize.py" "$RESULTS"/baseline_stats.csv "$RESULTS"/resilient_stats.csv
