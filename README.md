# AI Gateway

> A unified LLM gateway with intelligent routing, automatic failover, semantic
> caching, and real-time observability — built in Go.

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-required-DC382D?style=flat&logo=redis&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green?style=flat)

Route a single API request across OpenAI and Anthropic with per-key rate
limiting, vector-similarity caching, circuit breakers, and a live metrics
dashboard — without changing your application code.

---

## How a request flows

```text
                        ┌─────────────────────────────────────────────────────┐
POST /v1/chat           │                    AI Gateway                       │
─────────────────────▶  │                                                     │
  X-API-Key: •••        │  1. Auth          verify X-API-Key header           │
  provider / task       │  2. Cache lookup  cosine similarity ≥ 0.95?         │
  model                 │     └─ HIT  ────▶ return cached response            │
  messages              │     └─ MISS ────▶ resolve provider list             │
                        │  3. Rate limit    sliding 60-s TPM window (Redis)   │
                        │  4. Call provider Chat() or ChatStream()             │
                        │     └─ 5xx/429 ─▶ try next provider in list        │
                        │  5. Commit tokens correct reservation → actual cost  │
                        │  6. Cache store   async write-back to Qdrant        │
                        │  7. Emit metric   tokens · cost · latency · errors  │
                        └─────────────────────────────────────────────────────┘
```

---

## Features

### Routing & Reliability

| Feature | Details |
| --- | --- |
| **Unified endpoint** | `POST /v1/chat` — JSON response or SSE stream, same interface for all providers |
| **Task-based aliasing** | Name a logical task (`fast-chat`, `coding`) and map it to an ordered `[provider, model]` fallback list in YAML |
| **Automatic failover** | Retriable errors (5xx, 429, open circuit) silently advance to the next provider; 4xx errors fail immediately |
| **Circuit breaker** | Per-provider Closed → Open → HalfOpen state machine; configurable failure threshold and cooldown |
| **Streaming failover** | SSE failover is possible until the first content byte is flushed; after that, the client is committed |

### Cost & Performance

| Feature | Details |
| --- | --- |
| **Semantic cache** | Embeds prompts with `text-embedding-3-small`, stores vectors in Qdrant; cosine similarity ≥ 0.95 returns a cached response |
| **Rate limiting** | Sliding-window token-per-minute limiter per API key; `Reserve` pre-allocates, `Commit` corrects to actual usage |
| **Cost tracking** | Per-request `CostUSD` from a hardcoded pricing table covering GPT-4o, GPT-4o mini, Claude Sonnet, Opus, and Haiku |

### Observability

| Endpoint | Description |
| --- | --- |
| `GET /v1/metrics?window=5m` | Aggregated snapshot — request count, tokens, cost, cache rates, latency p50/p95, per-provider breakdown |
| `GET /v1/metrics/stream` | Live SSE feed — one `MetricEvent` per request, pushed to all connected subscribers |
| `GET /health` | Unauthenticated liveness probe |

All metrics endpoints require `X-API-Key`. Latency is reservoir-sampled
(1 000 samples/bucket) with 1-minute buckets and 1-hour retention.

---

## Quickstart

**Prerequisites:** Go 1.22+, Redis. Everything else is opt-in.

```bash
# Zero-config start — mock provider only, no API keys needed
REDIS_URL=redis://localhost:6379 go run ./cmd/gateway

# Full setup
OPENAI_API_KEY=sk-...        \
ANTHROPIC_API_KEY=sk-ant-... \
QDRANT_URL=http://localhost:6333  \
ALIAS_CONFIG=config/aliases.yaml  \
go run ./cmd/gateway
```

```bash
# Run all tests (no external Redis — uses miniredis)
go test ./...
go test ./... -race   # with data-race detector
```

### Dashboard

```bash
# Terminal 1 — gateway
go run ./cmd/gateway

# Terminal 2 — dashboard dev server (proxies /v1/* to :8080)
cd dashboard && npm install && npm run dev
```

Open `http://localhost:5173` and enter any non-empty string as the API key.

---

## Usage

### Direct provider request

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "X-API-Key: my-key" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "openai",
    "model":    "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

### Task-based request with automatic failover

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "X-API-Key: my-key" \
  -H "Content-Type: application/json" \
  -d '{
    "task":     "fast-chat",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

The response always includes `resolved_provider` indicating which backend
answered. Add `"stream": true` to either form for a Server-Sent Events response.

A cache hit returns `"resolved_provider": "cache"` and `"cache_hit": true`; the
token cost is still counted against the rate limit.

### Alias config

```yaml
# config/aliases.yaml
tasks:
  fast-chat:
    - provider: openai
      model: gpt-4o-mini
    - provider: anthropic          # automatic fallback on failure
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

---

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `REDIS_URL` | `redis://localhost:6379` | Redis connection string |
| `TPM_LIMIT` | `60000` | Token-per-minute cap per API key |
| `OPENAI_API_KEY` | — | Enables OpenAI provider; also required for semantic cache |
| `ANTHROPIC_API_KEY` | — | Enables Anthropic provider |
| `ALIAS_CONFIG` | — | Path to alias YAML; task routing disabled if unset |
| `QDRANT_URL` | — | Qdrant REST endpoint; semantic cache disabled if unset |
| `QDRANT_API_KEY` | — | Qdrant Cloud auth token (optional) |
| `CACHE_TTL` | `3600` | Semantic cache TTL in seconds |
| `CB_FAILURE_THRESHOLD` | — | Consecutive failures before tripping a circuit breaker (`0` = disabled) |
| `CB_COOLDOWN_SECONDS` | `60` | Seconds a tripped circuit stays open before allowing a probe request |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed CORS origin for browser clients (e.g. the dashboard) |

---

## Project layout

```text
cmd/gateway/              ← entry point; wires all components into the Gin router
config/
  aliases.example.yaml    ← copy this and set ALIAS_CONFIG
dashboard/                ← React + Recharts metrics dashboard (Vite)
internal/
  alias/                  ← task-name → [provider, model] resolver
  api/                    ← /chat handler, /metrics snapshot, /metrics/stream SSE
  cache/                  ← semantic cache (OpenAI embeddings + Qdrant)
  metrics/                ← MetricEvent, in-memory Store, pricing table
  provider/               ← Provider interface, OpenAI, Anthropic, Mock, CircuitBreaker
  ratelimit/              ← sliding-window TPM limiter + AuthMiddleware
pkg/models/               ← shared ChatRequest / ChatResponse / StreamEvent types
```
