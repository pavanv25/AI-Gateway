# AI Gateway

One endpoint to rule them all. Point it at OpenAI, Anthropic, or both — it'll
figure out who's alive, who's rate-limiting you, and who to blame when things
go wrong.

Built in Go. Backed by Redis. Comes with a live dashboard.

---

## What it does

You send one chat request. The gateway:

1. **Checks the semantic cache** — if someone asked basically the same thing
   recently, it returns the cached answer in microseconds and saves you money.
2. **Enforces rate limits** — sliding 60-second token window per API key,
   backed by Redis. No surprises on your bill.
3. **Routes to the right provider** — directly (`provider: openai`) or via a
   named task (`task: fast-chat`) that maps to an ordered fallback list.
4. **Retries on failure** — if a provider returns 5xx or 429, the gateway
   quietly tries the next one. You never see the error.
5. **Tracks everything** — tokens, cost, latency, cache hits, errors. Stream
   it live to the dashboard or poll for aggregated snapshots.

```text
POST /v1/chat  →  Auth  →  Semantic Cache
                                │
                           hit ─┘  miss ──→  resolveEntries  →  Fallback Loop
                                                                  │ Reserve (rate limit)
                                                                  │ Provider.Chat / ChatStream
                                                                  │ Commit actual tokens
                                                                  │ retry next entry on 5xx/429
                                                                  └ AsyncStore to cache
```

---

## Features

| | |
| --- | --- |
| `POST /v1/chat` | Unified chat — JSON or SSE streaming |
| `GET /v1/metrics` | Aggregated snapshot: tokens, cost, latency p50/p95, cache rates |
| `GET /v1/metrics/stream` | Live SSE feed — one event per request, in real time |
| `GET /health` | Liveness probe (no auth) |
| **Rate limiting** | Sliding-window TPM per API key via Redis sorted set + Lua |
| **Semantic cache** | OpenAI embeddings → Qdrant vector search (cosine ≥ 0.95); per-key isolation; TTL |
| **Task aliasing** | `task: fast-chat` resolves to an ordered `[provider, model]` fallback list |
| **Circuit breaker** | Per-provider Closed/Open/HalfOpen state machine; trips on 5xx, 429, network errors |
| **Cost tracking** | Per-request `CostUSD` from a pricing table; reservoir-sampled p50/p95 latency |
| **Live dashboard** | React + Recharts; rate chart, latency bars, provider breakdown, event log |

---

## Quickstart

You'll need Redis. Everything else is optional.

```bash
# Minimal — uses the mock provider, no API keys needed
REDIS_URL=redis://localhost:6379 go run ./cmd/gateway

# Full stack — real providers, semantic cache, aliases
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
QDRANT_URL=http://localhost:6333
ALIAS_CONFIG=config/aliases.yaml
go run ./cmd/gateway
```

```bash
# Tests — no Redis required (miniredis)
go test ./...
```

### + Dashboard

```bash
# Terminal 1
go run ./cmd/gateway

# Terminal 2
cd dashboard && npm install && npm run dev
```

Open `http://localhost:5173`. Enter any non-empty string as the API key.
Vite proxies `/v1/*` to the gateway — no CORS setup needed locally.

---

## Usage

### Direct request

Pick a provider and model explicitly:

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

### Task-based request

Let the gateway decide:

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

`task` resolves to an ordered `[provider, model]` list from your alias config.
The gateway tries each entry in order. Retriable errors (5xx, 429, open circuit)
move to the next entry silently. The response always includes `resolved_provider`
so you know who answered.

Add `"stream": true` to either form for SSE. Streaming failover is only possible
before the first content chunk is flushed — after that, you're committed.

A cache hit returns `"resolved_provider": "cache"` and `"cache_hit": true`.
Tokens are still counted against the rate limit.

### Alias config

Copy `config/aliases.example.yaml` and point `ALIAS_CONFIG` at it:

```yaml
tasks:
  fast-chat:
    - provider: openai
      model: gpt-4o-mini
    - provider: anthropic          # fallback if OpenAI is having a moment
      model: claude-haiku-4-5-20251001

  coding:
    - provider: anthropic
      model: claude-sonnet-4-6
    - provider: openai
      model: gpt-4o
```

---

## Environment variables

| Variable | Default | |
| --- | --- | --- |
| `REDIS_URL` | `redis://localhost:6379` | Required |
| `TPM_LIMIT` | `60000` | Tokens per minute per API key |
| `OPENAI_API_KEY` | — | Enables OpenAI provider + semantic cache embeddings |
| `ANTHROPIC_API_KEY` | — | Enables Anthropic provider |
| `ALIAS_CONFIG` | — | Path to alias YAML; task routing disabled if unset |
| `QDRANT_URL` | — | Qdrant REST endpoint; semantic cache disabled if unset |
| `QDRANT_API_KEY` | — | Qdrant Cloud auth token |
| `CACHE_TTL` | `3600` | Semantic cache TTL in seconds |
| `CB_FAILURE_THRESHOLD` | — | Consecutive failures before tripping a circuit breaker; `0` = disabled |
| `CB_COOLDOWN_SECONDS` | `60` | How long a tripped circuit stays open before a probe is allowed |

---

## Project layout

```text
cmd/gateway/          entry point — wires everything together
config/
  aliases.example.yaml
dashboard/            React + Recharts (Vite) — see dashboard/README.md
internal/
  alias/              task → [provider, model] resolver
  api/                routes: /chat, /metrics, /metrics/stream
  cache/              semantic cache (embeddings + Qdrant)
  metrics/            MetricEvent, in-memory Store, pricing table
  provider/           Provider interface, OpenAI, Anthropic, Mock, CircuitBreaker
  ratelimit/          sliding-window TPM limiter + AuthMiddleware
pkg/models/           shared request/response types
```
