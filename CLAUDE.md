# CLAUDE.md

## Project Overview

Go-based AI Gateway that routes requests between multiple LLM providers (OpenAI, Anthropic) through a unified API. Enforces per-API-key token-per-minute rate limiting via Redis.

## Key Commands

```bash
go run ./cmd/gateway          # run the server (port 8080)
go build -o bin/gateway ./cmd/gateway
go test ./...                 # all tests (no external Redis needed — miniredis)
go test ./internal/ratelimit/... -v -race
go mod tidy                   # sync dependencies
```

Environment variables:
- `TPM_LIMIT` — tokens per minute cap, default `60000`
- `REDIS_URL` — Redis connection string, default `redis://localhost:6379`
- `OPENAI_API_KEY` — enables the OpenAI provider (optional)
- `ANTHROPIC_API_KEY` — enables the Anthropic provider (optional)
- `ALIAS_CONFIG` — path to a YAML alias config file (optional; alias feature disabled if unset)

## Project Layout

```
cmd/gateway/              entry point — wires Redis, Limiter, providers, alias resolver, Gin router
config/
  aliases.example.yaml    example alias config; copy and set ALIAS_CONFIG to use
internal/
  alias/
    alias.go              Resolver — loads YAML, maps task names to ordered Entry lists
    alias_test.go
  api/routes.go           RegisterRoutes; POST /v1/chat handler with fallback loop
  provider/
    provider.go           Provider interface + ProviderError + IsRetriable
    mock.go               MockProvider — word-by-word streaming, no external calls
    mock_test.go
    openai.go             OpenAI provider (Chat + ChatStream via openai-go SDK)
    openai_test.go
    anthropic.go          Anthropic provider (Chat + ChatStream via anthropic-sdk-go)
    anthropic_test.go
  ratelimit/
    limiter.go            Sliding-window TPM limiter (Redis sorted set + Lua scripts)
    middleware.go         AuthMiddleware — extracts X-API-Key header, 401 if absent
    limiter_test.go
pkg/models/models.go      ChatRequest (+ Task), ChatResponse (+ ResolvedProvider), StreamEvent
```

## Scaffolding Rules

- **Interface First** — define or extend `Provider` before implementing a new backend
- **Dependency Injection** — pass `*redis.Client`, `Config`, and `*Limiter` into handlers; no global state
- **Strict Typing** — use `pkg/models` for all data crossing package boundaries; no `map[string]interface{}`
- **Standard Library** — favor stdlib + established packages (Gin, go-redis); no new framework introductions

## What Not To Include (yet)

- No cost/latency routing — use the hardcoded provider map toggle for now
- No multi-tenancy — no user accounts, DB key persistence, or organizations
- No observability sprawl — no OpenTelemetry, Prometheus, or Grafana; `log.Printf` is enough
- No response caching — Redis is scoped to rate limiting only
- No advanced retries — no exponential backoff or circuit breakers; alias failover is a linear ordered fallback only

---

For active development state — what's done, what's in progress, and what's next — see [PROGRESS.md](./PROGRESS.md).
