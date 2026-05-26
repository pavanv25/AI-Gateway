# AI Gateway

A Go-based API gateway that routes requests to multiple LLM providers through a unified interface, with per-API-key token-per-minute (TPM) rate limiting backed by Redis and semantic response caching backed by Qdrant.

## Architecture

```
POST /v1/chat  →  AuthMiddleware  →  Semantic Cache (Lookup)
                                           │
                                    hit ───┘  miss ──→  resolveEntries  →  Fallback Loop:
                                                                             Reserve (rate limit)
                                                                             → Provider.Chat/ChatStream
                                                                             → Commit actual tokens
                                                                             → retry next entry on 5xx/429
                                                                             → Cache (AsyncStore)
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
| **Semantic cache** | Embeds prompts via OpenAI, stores/retrieves responses in Qdrant by cosine similarity (threshold 0.95); per-tenant isolation via hashed API key; configurable TTL |
| **Task-based aliasing** | `task` field resolves to an ordered provider/model fallback list from a YAML config |
| **Fallback loop** | On retriable error (5xx/429), advances to the next alias entry; 4xx fails immediately |
| **OpenAI provider** | Calls `/v1/chat/completions`; supports streaming |
| **Anthropic provider** | Calls `/v1/messages`; supports streaming |
| **Mock provider** | Word-by-word streaming, no external calls — used in tests |

## Project Layout

```
cmd/gateway/              entry point — wires Redis, Limiter, providers, alias resolver, cache, Gin router
config/
  aliases.example.yaml    example alias config; copy and set ALIAS_CONFIG to use
internal/
  alias/
    alias.go              Resolver — loads YAML, maps task names to ordered Entry lists
  api/routes.go           RegisterRoutes; chat handler with cache lookup, fallback loop, async cache store
  cache/
    cache.go              Cache interface
    semantic.go           SemanticCache — OpenAI embeddings + Qdrant REST
  provider/
    provider.go           Provider interface + ProviderError + IsRetriable
    openai.go             OpenAI implementation
    anthropic.go          Anthropic implementation
    mock.go               MockProvider for testing
  ratelimit/
    limiter.go            Sliding-window TPM limiter (Redis sorted set + Lua)
    middleware.go         AuthMiddleware — requires X-API-Key header
pkg/models/models.go      Shared types: ChatRequest (+ Task), ChatResponse (+ ResolvedProvider, CacheHit), StreamEvent
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
| `OPENAI_API_KEY` | — | Enables the OpenAI provider (also required for semantic cache embeddings) |
| `ANTHROPIC_API_KEY` | — | Enables the Anthropic provider |
| `ALIAS_CONFIG` | — | Path to alias YAML config file; alias feature disabled if unset |
| `QDRANT_URL` | — | Qdrant REST endpoint; semantic cache disabled if unset |
| `QDRANT_API_KEY` | — | Qdrant Cloud auth token (optional) |
| `CACHE_TTL` | `3600` | Semantic cache entry lifetime in seconds |

## Usage

### Direct provider request

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

### Task-based request (with automatic failover)

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "X-API-Key: my-key" \
  -H "Content-Type: application/json" \
  -d '{
    "task": "fast-chat",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

The `task` field resolves to an ordered list of `{provider, model}` entries from your alias config. The gateway tries each entry in order, falling back on retriable errors (5xx, 429). The response includes `resolved_provider` indicating which backend handled the request.

Set `"stream": true` in either form to receive a Server-Sent Events response. For streaming, failover is only possible before the first content chunk is sent to the client.

A semantic cache hit returns `"resolved_provider": "cache"` and `"cache_hit": true` — no provider call is made, but the token cost is still counted against the rate limit.

### Alias config format

Copy `config/aliases.example.yaml` and point `ALIAS_CONFIG` at it:

```yaml
tasks:
  fast-chat:
    - provider: openai
      model: gpt-4o-mini
    - provider: anthropic
      model: claude-haiku-4-5-20251001

  coding:
    - provider: anthropic
      model: claude-sonnet-4-6
    - provider: openai
      model: gpt-4o
```

```bash
ALIAS_CONFIG=config/aliases.yaml go run ./cmd/gateway
```
