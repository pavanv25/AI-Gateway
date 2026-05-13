# AI Gateway

A Go-based API gateway that routes requests to multiple LLM providers through a unified interface, with per-API-key token-per-minute (TPM) rate limiting backed by Redis.

## Architecture

```
POST /v1/chat  →  AuthMiddleware  →  Rate Limiter (Reserve)  →  Provider  →  Commit actual tokens
```

**Provider interface** (`internal/provider/provider.go`):
```go
type Provider interface {
    Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error)
    ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error)
    Name() string
}
```

## What's Built

| Component | Description |
|---|---|
| `POST /v1/chat` | Unified chat endpoint — JSON response or SSE stream |
| `GET /health` | Unauthenticated liveness probe |
| **Rate limiter** | Sliding 60-second window per `X-API-Key`; `Reserve` holds `max_tokens`, `Commit` corrects to actual usage |
| **OpenAI provider** | Calls `/v1/chat/completions`; supports streaming |
| **Anthropic provider** | Calls `/v1/messages`; supports streaming |
| **Mock provider** | Word-by-word streaming, no external calls — used in tests |

## Project Layout

```
cmd/gateway/          entry point — wires Redis, Limiter, providers, Gin router
internal/
  api/routes.go       RegisterRoutes; chat handler (non-streaming + SSE)
  provider/
    provider.go       Provider interface
    openai.go         OpenAI implementation
    anthropic.go      Anthropic implementation
    mock.go           MockProvider for testing
  ratelimit/
    limiter.go        Sliding-window TPM limiter (Redis sorted set + Lua)
    middleware.go     AuthMiddleware — requires X-API-Key header
pkg/models/models.go  Shared types: ChatRequest, ChatResponse, StreamEvent
```

## Running

```bash
# Run (requires Redis)
REDIS_URL=redis://localhost:6379 go run ./cmd/gateway

# Test (uses miniredis — no Redis needed)
go test ./...
```

**Environment variables:**

| Variable | Default | Description |
|---|---|---|
| `REDIS_URL` | `redis://localhost:6379` | Redis connection |
| `TPM_LIMIT` | `60000` | Tokens per minute per API key |
| `OPENAI_API_KEY` | — | OpenAI credentials |
| `ANTHROPIC_API_KEY` | — | Anthropic credentials |

## Usage

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "X-API-Key: my-key" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "openai",
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

Set `"stream": true` to receive a Server-Sent Events response.
