# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go-based AI Gateway that routes requests between multiple LLM providers (OpenAI, Anthropic) through a unified API. Enforces per-API-key token-per-minute rate limiting via Redis.

## Key Commands

```bash
go run ./cmd/gateway                                     # run the server (port 8080)
go build -o bin/gateway ./cmd/gateway
go test ./...                                            # all tests (no external Redis needed — miniredis)
go test ./internal/ratelimit/... -v -race                # single package with race detector
go test ./internal/provider/... -run TestMockProvider -v # single test by name
go mod tidy
```

Endpoints: `POST /v1/chat` (requires `X-API-Key` header), `GET /health`.

Environment variables:
- `TPM_LIMIT` — tokens per minute cap, default `60000`
- `REDIS_URL` — Redis connection string, default `redis://localhost:6379`
- `OPENAI_API_KEY` — enables the OpenAI provider (optional)
- `ANTHROPIC_API_KEY` — enables the Anthropic provider (optional)
- `ALIAS_CONFIG` — path to a YAML alias config file (optional; alias feature disabled if unset)
- `CB_FAILURE_THRESHOLD` — consecutive failures (5xx, 429, network) to open a circuit breaker per provider; unset or `0` disables circuit breakers (opt-in)
- `CB_COOLDOWN_SECONDS` — seconds a circuit stays Open before allowing a single probe request (default `60` when threshold is set)

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
    provider.go           Provider interface + ProviderError + IsRetriable + ErrCircuitOpen
    circuit.go            CircuitBreaker — 3-state (Closed/Open/HalfOpen) Provider wrapper
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

## Request Lifecycle

Every `POST /v1/chat` passes through these stages in order:

1. **Auth** (`ratelimit.AuthMiddleware`) — extracts `X-API-Key`; 401 if absent. Key is stored in Gin context under `ratelimit.APIKeyContextKey`.
2. **Entry resolution** (`resolveEntries`) — if `task` is set, `alias.Resolver.Resolve` returns an ordered `[]alias.Entry{provider, model}`. If `task` is absent, a single entry is built from `provider`+`model` fields directly.
3. **Fallback loop** (per entry, in order):
   - `limiter.Reserve` — atomically checks sliding-window capacity and writes a reservation (`id:maxTokens` member) to the Redis sorted set. Returns `ErrLimitExceeded` (→ 429) or an opaque token.
   - `p.Chat` / `p.ChatStream` — calls the upstream provider.
   - On success: `limiter.Commit` replaces the reservation with actual token count. Response includes `resolved_provider`.
   - On retriable error (5xx, HTTP 429): Commit 0 tokens, log, advance to next entry.
   - On non-retriable error (4xx, context cancel): break immediately.
4. **Streaming failover constraint** — SSE headers are flushed lazily on the first content delta. Failover to the next entry is only possible if the error occurs *before* any content is sent to the client (`contentSent` flag in `handleStreamWithFallback`).

The `mock` provider is always registered (no API key needed) and is the only provider available without env vars — useful for local testing.

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
- No advanced retries — no exponential backoff; alias fallback with per-provider circuit breakers is the retry strategy

---

For active development state — what's done, what's in progress, and what's next — see [PROGRESS.md](./PROGRESS.md).
