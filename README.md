# TokenGate

TokenGate is a token-aware gateway for shared LLM inference servers.

When many applications share the same LLM servers, request counts are a poor measure of load: one reply may be a few tokens and another thousands. TokenGate limits each application by the tokens it actually uses, routes each request to the least busy backend, and keeps failed backends out of rotation. It runs as several stateless Go gateway replicas that share token budgets through Redis, so it can scale horizontally.

This is the final project for our distributed systems course at Northeastern University (Fall 2026).

## Team

| Member |
|---|
| Nikhil Kundalli Harish |
| Nikhil Shreeshail Hugar |
| Nisha Murthy Dinesh |

## Repository layout

| Path | Contents |
|---|---|
| `cmd/gateway`, `cmd/mockbackend` | Entry points for the gateway and the mock LLM backend |
| `cmd/rlbench` | Benchmark for rate-limiter overshoot under concurrency |
| `internal/api` | Request, response and streaming formats shared by all components |
| `internal/gateway` | HTTP server, request handling, streaming proxy, retries |
| `internal/routing` | Backend pool and routing policies |
| `internal/health` | Active health checks |
| `internal/ratelimit` | Token budgets: atomic Redis (Lua scripts), naive Redis, local, fail modes |
| `internal/mock` | Mock LLM backend with fault injection |
| `internal/metrics` | Prometheus metric definitions |
| `internal/config` | Configuration loading |
| `configs` | Backends and per-application quotas |
| `deploy` | Dockerfiles and Docker Compose |
| `observability` | Prometheus and Grafana configuration |
| `loadtest` | Locust workloads |
| `experiments` | Experiment scripts and results |
| `docs` | Design contracts and reports |

## Getting started

Requirements: Go (version in `go.mod`), Docker with Docker Compose, Python 3.

```bash
make tidy                               # first time only: download dependencies
make check                              # format check, vet and tests (same as CI)
make up                                 # start the whole system (2 gateways)
pip install -r loadtest/requirements.txt
make loadtest                           # Locust web UI on http://localhost:8089
make down
```

Try one request by hand:

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' -H 'X-App-ID: support-bot' \
  -d '{"messages":[{"role":"user","content":"hello"}],"max_tokens":10}'
```

| Service | Address |
|---|---|
| Gateways (through nginx) | http://localhost:8080 |
| Grafana dashboard | http://localhost:3000 |
| Prometheus | http://localhost:9090 |
| Mock backends (fault injection) | http://localhost:9001, http://localhost:9002 |

Change settings with environment variables, for example `make up GATEWAYS=4 POLICY=round_robin`. All settings are listed in [docs/DESIGN.md](docs/DESIGN.md), section 8.

## Experiments

```bash
make experiment EXP=exp1_scaling     # 1 vs 2 gateway replicas
make experiment EXP=exp2_routing     # round robin vs least requests vs least tokens
make experiment EXP=exp3_ratelimit   # overshoot: naive vs atomic Redis limiter
make experiment EXP=exp4_faults      # backend failure with and without health checks and retries
```

Results are saved under `experiments/<experiment>/results/`.

## How we work

- One branch per task, named `area/short-description` (for example `infra/mock-backend`).
- Changes reach `main` only through a pull request reviewed by another team member.
- Changes to shared contracts (`internal/api`, `docs/DESIGN.md`, `configs`) need approval from all three members.

## Documentation

- [Design and shared contracts](docs/DESIGN.md)
