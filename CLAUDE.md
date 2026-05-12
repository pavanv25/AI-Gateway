# CLAUDE.md

## Project Overview

This is an Go-based AI Gateway that routes requests between multiple LLM providers through a unified API.

## Key Commands
- Init: go mod init <your-module-path>
- Run: `go run cmd/gateway/main.go`
- Test: `go test ./...`
- Lint: `golangci-lint run`

## Project Layout (Strict)
- `cmd/gateway/`: Application entry point.
- `internal/api/`: Gin handlers and route definitions.
- `internal/provider/`: Interfaces for LLM adapters.
- `internal/ratelimit/`: Redis logic.
- `pkg/models/`: Unified Request/Response structs.

## Scaffolding Rules
- **Interface First:** Define the `Provider` interface before implementing OpenAI/Anthropic.
- **Dependency Injection:** Pass the Redis client and Config into handlers; no global state.
- **Strict Typing:** Use `pkg/models` for all data crossing boundaries (don't use `map[string]interface{}`).
- **Standard Library:** Favor Go standard library or established packages (Gin, Go-Redis).

## Current Focus
- Initializing the Go module.
- Setting up the Gin router boilerplate.
- Defining the shared Request/Response data models.

## What Not To Include

- Do not write logic for cost/latency yet. Use a hardcoded ProviderA vs ProviderB toggle for now
- No Multi-Tenancy: Do not implement user accounts, database persistence for keys, or organizations.
- No Observability Sprawl: Avoid OpenTelemetry, Prometheus, or Grafana wrappers at this stage. Standard logs are enough.
- No Request Caching: Do not implement Redis caching for LLM responses yet; Redis should only be scoped for rate-limiting scaffolding.
- No Advanced Retries: Do not implement exponential backoff or complex circuit breakers. Stick to a basic "if fail, try other" skeleton.