# TokenGate design and shared contracts

This file holds the agreements every component depends on. Change it only through a pull request approved by the whole team.

## 1. Architecture

```
Locust clients
     |
Load balancer (nginx)
     |
Gateway 1 ... Gateway N   (stateless Go replicas)
     |              \
Redis (budgets)    Mock LLM backends
```

- Gateways keep no budget state in memory. All budget state lives in Redis, so any gateway can be added or removed at any time.
- Prometheus scrapes every gateway and every mock backend. Grafana shows the dashboards.

### Code areas

| Area | Paths |
|---|---|
| Gateway, routing, health, failover | `internal/gateway`, `internal/routing`, `internal/health`, `cmd/gateway` |
| Distributed rate limiting | `internal/ratelimit` |
| Mock backends, infrastructure, observability, load tests | `internal/mock`, `cmd/mockbackend`, `internal/metrics`, `deploy`, `observability`, `loadtest`, `experiments` |
| Shared contracts (changes need approval from the whole team) | `internal/api`, `configs`, this file |

## 2. Client API

### Request

`POST /v1/chat/completions`

| Header | Required | Meaning |
|---|---|---|
| `X-App-ID` | yes | The calling application. Must exist in `configs/quotas.yaml`. |
| `X-Request-ID` | no | Unique request ID. The gateway generates a UUID if missing. |

```json
{
  "messages": [{"role": "user", "content": "Summarize this document..."}],
  "max_tokens": 256,
  "stream": true
}
```

- `max_tokens`: integer, 1 to 4096. Default 256 if missing.
- `stream`: default `true`. Non-streaming is supported for simple testing.
- Prompt tokens are estimated as total characters of all `content` fields divided by 4, rounded up.

### Response headers (set by the gateway)

`X-Request-ID`, `X-Gateway-ID`, `X-Backend-ID`. These let experiments see which gateway and backend served each request.

### Errors

All errors return JSON: `{"error": "<reason>", "message": "<human readable text>"}`

| Status | `error` value | When |
|---|---|---|
| 400 | `bad_request` | Invalid JSON, missing `messages`, `max_tokens` out of range |
| 401 | `unknown_app` | Missing or unknown `X-App-ID` |
| 429 | `over_budget` | Not enough tokens in the app's budget right now. Includes `Retry-After` header (seconds). |
| 429 | `exceeds_capacity` | Request cost is larger than the app's total capacity, so it can never succeed |
| 502 | `backend_error` | Backend failed and the retry also failed |
| 503 | `no_backend` | No healthy backend available |
| 503 | `limiter_unavailable` | Redis unreachable and fail mode is `fail_closed` |

### Other gateway endpoints

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Returns 200 when the gateway can serve. Used by nginx. |
| `GET /metrics` | Prometheus metrics |

## 3. Streaming format

Server-sent events (`Content-Type: text/event-stream`). Each event is one `data:` line followed by a blank line.

Token event:
```
data: {"token": "tok17", "index": 17}
```

Final event (always last on success):
```
data: {"done": true, "usage": {"prompt_tokens": 12, "completion_tokens": 120}}
```

Error event (backend fails after tokens were already sent):
```
data: {"done": true, "error": "backend_error", "usage": {"prompt_tokens": 12, "completion_tokens": 40}}
```

- The gateway forwards token events to the client unchanged and counts them.
- The gateway settles the reservation using `prompt_tokens + completion_tokens` from the final event. If the stream breaks with no final event, it settles using the number of token events it counted.
- Non-streaming response: `{"id": "<request id>", "content": "<all tokens joined by spaces>", "usage": {...}}`

## 4. Mock backend

Same request format and streaming format as the gateway. Runs on port 9000.

### Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `MOCK_ID` | hostname | Backend ID used in metrics and `X-Backend-ID` |
| `PORT` | 9000 | Listen port |
| `TOKENS_PER_SEC` | 50 | Generation speed per request |
| `TTFT_MS` | 200 | Delay before the first token |
| `OUTPUT_MIN_FRAC` | 0.2 | Reply length is a random fraction of `max_tokens` between these two values |
| `OUTPUT_MAX_FRAC` | 1.0 | |
| `SEED` | 42 | Random seed, so runs are reproducible |
| `MAX_CONCURRENCY` | 32 | Requests generated at the same time. Extra requests wait in a queue. |
| `MAX_QUEUE` | 128 | Queue limit. Beyond this the backend returns 503. |

### Fault injection endpoints

| Endpoint | Effect |
|---|---|
| `POST /admin/slow?factor=4` | Multiplies all delays by `factor` |
| `POST /admin/fail?mode=error` | Every request returns 503 immediately; `/healthz` returns 503 |
| `POST /admin/fail?mode=hang` | Requests are accepted but never answered (tests timeouts); `/healthz` still returns 200 |
| `POST /admin/recover` | Back to normal |

Also exposes `GET /healthz` and `GET /metrics`.

## 5. Go interfaces

### Limiter

```go
type Limiter interface {
    // Reserve holds cost tokens for appID. Returns false if the budget is too low.
    // A non-nil error means the budget store could not be reached; the gateway
    // then applies the configured fail mode.
    Reserve(ctx context.Context, appID, reqID string, cost int) (bool, error)

    // Settle reports actual usage. Refunds reserved-used, or charges the extra if
    // used > reserved. Calling it twice with the same reqID has no extra effect.
    Settle(ctx context.Context, appID, reqID string, reserved, used int) error
}
```

Implementations, chosen with `LIMITER`:
- `redis`: shared atomic budgets (the main design)
- `local`: each gateway keeps its own bucket with capacity/N and refill/N
- `naive`: non-atomic read-then-write in Redis (baseline for the correctness experiment only)
- `none`: admits everything (to measure the system without rate limiting)

### Routing

```go
type Backend struct {
    ID                string
    URL               string
    OpenRequests      atomic.Int64 // requests in flight from this gateway
    OutstandingTokens atomic.Int64 // reserved tokens in flight from this gateway
}

type Policy interface {
    Name() string
    Pick(healthy []*Backend) *Backend // nil if the list is empty
}
```

Implementations, chosen with `POLICY`: `round_robin`, `least_requests`, `least_tokens`.

Each gateway only sees its own in-flight load. Gateways do not share load information.

## 6. Rate limiting

- Algorithm: token bucket per application, with `capacity` (maximum burst) and `refill_per_sec`.
- Cost of a request: estimated prompt tokens + `max_tokens`. Reserved before forwarding.
- Settle: refund `reserved - used`, never above capacity. If `used > reserved`, the extra is charged and the bucket may go below zero; this is recorded as overshoot.
- The bucket is refilled lazily inside the Lua script on every reserve and settle.
- Clock: the Lua script uses Redis `TIME`. For the clock drift experiment, `CLOCK_SOURCE=local` passes the gateway's own time (plus `CLOCK_SKEW_MS`) to the script instead.
- Redis timeout: 50 ms per call. A timeout counts as Redis unreachable.

### Fail modes (`FAIL_MODE`)

| Mode | Behavior when Redis is unreachable |
|---|---|
| `fail_closed` (default) | Reject with 503 `limiter_unavailable`. Protects cost, loses availability. |
| `fail_open` | Admit every request with no accounting. Keeps availability, risks overspending. |
| `fail_local` | Fall back to a local bucket with capacity/N and refill/N. |

### Redis keys

| Key | Type | Contents |
|---|---|---|
| `budget:{app_id}` | hash | `tokens` (current balance), `ts` (last refill time, seconds) |
| `settled:{request_id}` | string, TTL 10 minutes | Marker that makes settle idempotent |

Capacity and refill rate come from `configs/quotas.yaml` and are passed into the Lua script as arguments, so Redis never holds a copy that could go out of sync.

## 7. Health checks, circuit breaker and retries

| Setting | Value |
|---|---|
| Active health check | `GET /healthz` on every backend every 2 s, timeout 500 ms |
| Marked unhealthy | After 2 consecutive failed checks |
| Marked healthy again | After 2 consecutive successful checks |
| Circuit breaker opens | After 5 consecutive request failures to a backend |
| Breaker open time | 10 s, then half-open: one trial request decides closed or open again |
| Retries | At most 1, on a different backend, and only if no token has reached the client yet |
| Backend timeouts | Connect 1 s; first token 10 s; gap between tokens 5 s |

A backend receives traffic only if it is healthy and its breaker is not open.

## 8. Configuration

Switches are environment variables, so Docker Compose and experiment scripts can change them easily. Lists live in YAML files.

### Gateway environment variables

| Variable | Default | Values |
|---|---|---|
| `GATEWAY_ID` | hostname | |
| `PORT` | 8000 | |
| `POLICY` | `least_tokens` | `round_robin`, `least_requests`, `least_tokens` |
| `LIMITER` | `redis` | `redis`, `local`, `naive`, `none` |
| `FAIL_MODE` | `fail_closed` | `fail_closed`, `fail_open`, `fail_local` |
| `REDIS_ADDR` | `redis:6379` | |
| `GATEWAY_COUNT` | 1 | Used by `local` and `fail_local` to split budgets |
| `CLOCK_SOURCE` | `redis` | `redis`, `local` |
| `CLOCK_SKEW_MS` | 0 | Only used when `CLOCK_SOURCE=local` |
| `HEALTH_CHECKS` | `true` | `false` turns off active health checks (baseline for the fault experiment) |
| `MAX_RETRIES` | 1 | Retries on another backend before the first token; 0 turns retries off |
| `BACKENDS_FILE` | `configs/backends.yaml` | |
| `QUOTAS_FILE` | `configs/quotas.yaml` | |

### `configs/backends.yaml`

```yaml
backends:
  - id: backend-fast
    url: http://backend-fast:9000
  - id: backend-slow
    url: http://backend-slow:9000
```

### `configs/quotas.yaml`

```yaml
apps:
  support-bot:
    capacity: 20000
    refill_per_sec: 500
  summarizer:
    capacity: 50000
    refill_per_sec: 1000
  heavy-batch:
    capacity: 20000
    refill_per_sec: 500
```

Requests from apps not listed here get 401 `unknown_app`.

`configs/quotas-loadtest.yaml` has the same apps with very large budgets. The scaling and routing experiments use it so budgets never reject requests.

## 9. Metrics (definitions in `internal/metrics`)

Labels: `gateway_id`, `app_id`, `backend`, `policy` where they apply. Never use request IDs as labels.

### Gateway

| Metric | Type | Labels |
|---|---|---|
| `tg_requests_total` | counter | `gateway_id`, `app_id`, `status` |
| `tg_tokens_total` | counter | `gateway_id`, `app_id`, `type` = `reserved`, `used`, `refunded`, `overshoot` |
| `tg_request_duration_seconds` | histogram | `gateway_id`, `app_id`, `backend` |
| `tg_ttft_seconds` | histogram | `gateway_id`, `backend` |
| `tg_rejections_total` | counter | `gateway_id`, `app_id`, `reason` (the `error` values in section 2) |
| `tg_retries_total` | counter | `gateway_id`, `backend` |
| `tg_backend_open_requests` | gauge | `gateway_id`, `backend` |
| `tg_backend_outstanding_tokens` | gauge | `gateway_id`, `backend` |
| `tg_backend_healthy` | gauge (0 or 1) | `gateway_id`, `backend` |
| `tg_breaker_state` | gauge (0 closed, 1 half-open, 2 open) | `gateway_id`, `backend` |
| `tg_redis_latency_seconds` | histogram | `gateway_id`, `op` = `reserve`, `settle` |
| `tg_redis_errors_total` | counter | `gateway_id`, `op` |

### Mock backend

| Metric | Type | Labels |
|---|---|---|
| `tg_mock_active_requests` | gauge | `backend` |
| `tg_mock_queue_depth` | gauge | `backend` |
| `tg_mock_tokens_generated_total` | counter | `backend` |
| `tg_mock_requests_total` | counter | `backend`, `status` |

Histogram buckets: request duration 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60 s. Time to first token 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 s.

## 10. Ports

| Component | Port |
|---|---|
| Load balancer (exposed on host) | 8080 |
| Gateway | 8000 |
| Mock backend | 9000 (inside Docker); 9001 and 9002 on the host for fault injection |
| Redis | 6379 |
| Prometheus | 9090 |
| Grafana | 3000 |
| Locust web UI | 8089 |

## 11. Deployment

- Local development and testing: Docker Compose (`deploy/compose`).
- Experiments: AWS EC2 instances running Docker, in `us-east-1` (AWS Academy Learner Lab). Gateways, Redis, backends and Locust workers run on separate instances where the Learner Lab limits allow; otherwise several containers share an instance, which is stated in the results.

## 12. Conventions

- Go version as in `go.mod`. Standard library `net/http` for the gateway and mock backend.
- Code must pass `make check` (gofmt, go vet, tests with the race detector) before merging.
- Branch names: `area/short-description`, for example `infra/mock-backend`, `gateway/round-robin`, `ratelimit/lua-scripts`.
