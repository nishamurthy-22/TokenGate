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
| `cmd/gateway`, `cmd/mockbackend` | Entry points for the two programs |
| `internal/api` | Request, response and streaming formats shared by all components |
| `internal/gateway` | HTTP server, request handling, streaming proxy |
| `internal/routing` | Backend pool and routing policies |
| `internal/health` | Health checks and circuit breakers |
| `internal/ratelimit` | Token budgets in Redis (Lua scripts), local variant, fail modes |
| `internal/mock` | Mock LLM backend with fault injection |
| `internal/metrics` | Prometheus metric definitions |
| `internal/config` | Configuration loading |
| `configs` | YAML configuration files |
| `deploy` | Dockerfiles, Docker Compose, AWS scripts |
| `observability` | Prometheus and Grafana configuration |
| `loadtest` | Locust workloads |
| `experiments` | Experiment runner, configs, results and analysis |
| `docs` | Design contracts and reports |

## Getting started

Requirements: Go (version in `go.mod`), Docker with Docker Compose, Python 3 for Locust.

```bash
make help     # list all commands
make check    # format check, vet and tests (same as CI)
make build    # build both binaries into ./bin
```

Running the full system with `make up` will be available once the Compose file is added.

## How we work

- One branch per task, named `area/short-description` (for example `infra/mock-backend`).
- Changes reach `main` only through a pull request reviewed by another team member.
- Changes to shared contracts (`internal/api`, `docs/DESIGN.md`, `configs`) need approval from all three members.

## Documentation

- [Design and shared contracts](docs/DESIGN.md)
