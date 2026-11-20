#!/usr/bin/env bash
# Runs one experiment: experiments/run_experiment.sh exp1_scaling
set -euo pipefail
EXP="${1:?usage: run_experiment.sh <exp1_scaling|exp2_routing|exp3_ratelimit|exp4_faults>}"
SCRIPT="$(cd "$(dirname "$0")" && pwd)/$EXP/run.sh"
if [ ! -f "$SCRIPT" ]; then
  echo "No run.sh found for $EXP" >&2
  exit 1
fi
bash "$SCRIPT"
