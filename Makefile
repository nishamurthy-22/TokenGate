# TokenGate Makefile
# Run `make` or `make help` to see all targets.

COMPOSE_FILE := deploy/compose/docker-compose.yml
COMPOSE      := docker compose -f $(COMPOSE_FILE)
BIN          := bin
PROFILE      ?= steady
EXP          ?= exp1_scaling
LB_URL       ?= http://localhost:8080
GATEWAYS     ?= 2

# Passed through to Docker Compose (see docs/DESIGN.md, section 8).
export GATEWAYS POLICY LIMITER FAIL_MODE HEALTH_CHECKS MAX_RETRIES QUOTAS_FILE CLOCK_SOURCE CLOCK_SKEW_MS

.DEFAULT_GOAL := help
.PHONY: help tidy build test test-redis vet fmt fmt-check check run-gateway run-mock up down logs ps loadtest experiment clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

## ---- Go ----
tidy: ## Download dependencies and update go.sum
	go mod tidy

build: ## Build gateway, mock backend and rlbench into ./bin
	go build -o $(BIN)/gateway ./cmd/gateway
	go build -o $(BIN)/mockbackend ./cmd/mockbackend
	go build -o $(BIN)/rlbench ./cmd/rlbench

test: ## Run all Go tests with the race detector
	go test -race ./...

test-redis: ## Also run the Redis tests (starts Redis with Docker Compose)
	$(COMPOSE) up -d redis
	TOKENGATE_TEST_REDIS=localhost:6379 go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go code
	gofmt -w .

fmt-check: ## Fail if any Go file is not formatted (same check as CI)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "Not formatted:"; echo "$$out"; exit 1; fi

check: fmt-check vet test ## Everything CI runs: format check, vet, tests

run-gateway: ## Run one gateway locally without Docker (needs Redis and backends)
	go run ./cmd/gateway

run-mock: ## Run one mock backend locally without Docker
	go run ./cmd/mockbackend

## ---- Local system (Docker Compose) ----
up: ## Start everything, e.g. make up GATEWAYS=4 POLICY=round_robin
	$(COMPOSE) up -d --build --remove-orphans
	$(COMPOSE) restart lb

down: ## Stop everything
	$(COMPOSE) down --remove-orphans

logs: ## Follow logs of all containers
	$(COMPOSE) logs -f

ps: ## List running containers
	$(COMPOSE) ps

## ---- Load tests and experiments ----
loadtest: ## Locust web UI on http://localhost:8089, e.g. make loadtest PROFILE=bursty
	PROFILE=$(PROFILE) locust -f loadtest/locustfile.py --host $(LB_URL)

experiment: ## Run an experiment, e.g. make experiment EXP=exp2_routing
	bash experiments/run_experiment.sh $(EXP)

clean: ## Remove build output
	rm -rf $(BIN)
