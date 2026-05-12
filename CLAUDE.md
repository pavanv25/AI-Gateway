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

## Project Layout

```
cmd/gateway/          entry point — wires Redis, Limiter, providers, Gin router
internal/
  api/routes.go       RegisterRoutes; POST /v1/chat handler (non-streaming + SSE)
  provider/
    provider.go       Provider interface (Chat, ChatStream, Name)
    mock.go           MockProvider — word-by-word streaming, no external calls
    mock_test.go
  ratelimit/
    limiter.go        Sliding-window TPM limiter (Redis sorted set + Lua scripts)
    middleware.go     AuthMiddleware — extracts X-API-Key header, 401 if absent
    limiter_test.go   8 unit tests via miniredis
pkg/models/models.go  ChatRequest, ChatResponse, Choice, Usage, StreamEvent
```

## What's Built

- **Provider interface** — `Chat` (blocking) + `ChatStream` (returns `<-chan StreamEvent`, producer closes)
- **MockProvider** — implements Provider; streams word-by-word; respects context cancellation
- **Rate limiter** — sliding 60-second window keyed on `X-API-Key`; `Reserve` atomically checks capacity and records `max_tokens`; `Commit` corrects to actual token count after response
- **POST /v1/chat** — parses `ChatRequest`, reserves tokens, calls provider, commits actual usage, returns JSON or SSE stream
- **GET /health** — unauthenticated liveness check

## What's Next

- `internal/provider/openai.go` — implement `Provider` for OpenAI (`/v1/chat/completions`)
- `internal/provider/anthropic.go` — implement `Provider` for Anthropic (`/v1/messages`)
- Wire `StreamEvent` `Usage` field so streaming `Commit` uses actual tokens (currently commits 0)
- Provider selector logic (beyond the current first-found fallback)

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
- No advanced retries — no exponential backoff or circuit breakers; basic error passthrough only
