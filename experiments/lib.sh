# Shared helpers for the experiment scripts. Source this file; do not run it.
# Locust must be installed: pip install -r loadtest/requirements.txt

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/deploy/compose/docker-compose.yml"
LB_URL="${LB_URL:-http://localhost:8080}"
USERS="${USERS:-50}"
SPAWN_RATE="${SPAWN_RATE:-10}"
DURATION="${DURATION:-60s}"

compose() {
  docker compose -f "$COMPOSE_FILE" "$@"
}

# Start the system with the current environment settings and wait until it serves.
start_system() {
  compose up -d --build --remove-orphans
  compose restart lb >/dev/null
  wait_ready
}

stop_system() {
  compose down --remove-orphans
}

wait_ready() {
  for _ in $(seq 1 60); do
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$LB_URL/v1/chat/completions" \
      -H 'Content-Type: application/json' -H 'X-App-ID: support-bot' \
      -d '{"messages":[{"role":"user","content":"hi"}],"max_tokens":4}' || true)
    if [ "$code" = "200" ]; then
      return 0
    fi
    sleep 2
  done
  echo "System did not become ready at $LB_URL" >&2
  exit 1
}

# run_locust <output prefix> [extra locust arguments]
run_locust() {
  local out="$1"
  shift
  (cd "$ROOT" && PROFILE="${PROFILE:-steady}" locust -f loadtest/locustfile.py --headless \
    -u "$USERS" -r "$SPAWN_RATE" -t "$DURATION" --host "$LB_URL" \
    --csv "$out" --only-summary "$@")
}
