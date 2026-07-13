# PROGRESS.md

Active development state. See CLAUDE.md for static technical context.

---

## Completed

### Rate Limiting & Auth

- Sliding 60-second window TPM limiter keyed on `X-API-Key` via Redis sorted
  set + Lua scripts; `Reserve` atomically checks capacity, `Commit` corrects
  to actual usage after each response.
- `AuthMiddleware` extracts `X-API-Key` header; 401 if absent.

### Provider Layer

- `Provider` interface + `ProviderError{StatusCode, Cause}` + `IsRetriable`
  in `internal/provider/provider.go`; both OpenAI and Anthropic wrap SDK
  errors into `*ProviderError` so the handler classifies retriability without
  importing SDK types.
- OpenAI provider: `Chat` + `ChatStream` via openai-go SDK; streaming sets
  `IncludeUsage: true` for a final token-count chunk.
- Anthropic provider: `Chat` + `ChatStream`; extracts system messages into
  the top-level `system` field.
- `MockProvider`: word-by-word streaming, no external calls — always
  registered, useful for local testing without API keys.

### Task-Based Aliasing

- `internal/alias`: `Resolver` loads a YAML config at startup and maps task
  names to ordered `[]Entry{provider, model}` lists.
- Callers send `{"task": "fast-chat", ...}`; the gateway resolves to an
  ordered fallback list via `ALIAS_CONFIG`.
- Fallback loop in `routes.go`: retriable errors (5xx, 429) advance to the
  next entry; 4xx or context cancellation breaks immediately.
- Streaming failover constrained by `contentSent` flag — failover only
  possible before the first content chunk is flushed.
- Response includes `resolved_provider` showing which backend handled the
  request.

### Semantic Caching

- `internal/cache`: `Cache` interface + `SemanticCache` backed by Qdrant
  (REST) for vector storage and OpenAI `text-embedding-3-small` for prompt
  embeddings.
- Similarity threshold 0.95; per-key-hash namespacing; TTL-controlled expiry.
- `AsyncStore` writes back on the hot path via a bounded 64-slot goroutine
  semaphore.
- Cache lookup runs before the rate-limit check in `routes.go`; hits
  short-circuit provider fallback and return `resolved_provider: "cache"`.
- `CacheHit bool` added to `ChatResponse` in `pkg/models/models.go`.
- Enabled when `QDRANT_URL` + `OPENAI_API_KEY` are set; lazy init at startup.

### Circuit Breaker & Provider Cooldown

- 3-state machine (Closed / Open / HalfOpen) per provider, in-memory, goroutine-safe.
- Trips on consecutive 5xx, 429, and network errors; context cancellation does not count.
- `CircuitBreaker` wraps every registered `Provider` transparently via `provider.New(p, cfg)`.
- `ErrCircuitOpen` is retriable — fallback loop skips to the next alias entry automatically.
- Configurable via `CB_FAILURE_THRESHOLD` and `CB_COOLDOWN_SECONDS`; opt-in (unset = disabled).

### Metrics & Cost Tracking

- `internal/metrics`: `MetricEvent` struct + `Collector` interface; `NoopCollector` default.
- `chatHandler` emits one `MetricEvent` per request — provider, model, latency, token counts,
  cache-hit flag, fallback attempts, and `CostUSD`.
- `Store`: thread-safe in-memory aggregator using 1-minute buckets (60 retained, 1h window).
  Latency slices reservoir-sampled at 1000/bucket; p50/p95 computed at query time.
  Bucket map keyed on `int64` UTC Unix timestamp to avoid `time.Time` equality traps.
  Eviction runs on `Query()` to keep the `Record()` hot path fast.
- `EstimateCost`: hardcoded pricing table for 5 models (gpt-4o, gpt-4o-mini,
  claude-sonnet-4-6, claude-opus-4-7, claude-haiku-4-5-20251001). Unknown models log
  a one-time warning via `sync.Map`; mock/cache providers return $0.
- Cache-hit cost uses `cached.ResolvedProvider` + `cached.Model` (not `req.Model`) so
  task-based requests with empty `req.Model` get accurate estimates.
- `metrics.NewStore()` wired as the live `Collector` in `cmd/gateway/main.go`.

### Metrics Dashboard

- `GET /metrics?window=Nm` JSON snapshot and `GET /metrics/stream` SSE endpoints in
  `internal/api/routes.go`, both auth-gated via `AuthMiddleware`; `Store.Query(window)`
  returns aggregated p50/p95 latency, token counts, cost, and per-provider breakdowns.
- `dashboard/`: standalone Vite + React + Recharts app. Components: `StatCard`,
  `BreakdownChart`, `LatencyChart`, `RequestRateChart`, `EventLog`. Snapshot polling via
  `useSnapshot`, live updates via `useSSEEvents`. Shared types in `types.ts`, API calls in
  `api.ts`, Vite dev-server proxy forwards `/metrics` to the Go gateway.
- CORS middleware (`gin-contrib/cors`) registered globally; `CORS_ORIGIN` env controls the
  allowed origin (default `http://localhost:5173`); preflight cached 12h.

## Next Steps

Dashboard milestone (Steps 1–6) is complete on `main`. No feature is currently scoped —
candidates for the next milestone: request logging, auth key management, or cost budget
enforcement.

---

## Session Log

### 2026-06-03 — Metrics foundation (Steps 1–3)

Implemented `MetricEvent`/`Collector` interface, thread-safe in-memory `Store` with
reservoir-sampled p50/p95, and hardcoded pricing table with per-request `CostUSD`
computation at all three `collector.Record` sites. Each step reviewed by a dedicated
reviewer subagent before execution.

### 2026-07-10 — Metrics endpoints, dashboard, CORS (Steps 4–6)

Added `GET /metrics` JSON snapshot and `GET /metrics/stream` SSE endpoints; scaffolded
the `dashboard/` Vite + React + Recharts app consuming both; added CORS middleware
(`CORS_ORIGIN` env) and README dev-setup docs. This closed out the dashboard milestone.

### 2026-07-13 — Repo hygiene sweep

- Removed 6 stale worktrees and their branches (`circuit-breaker`,
  `cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`,
  `jovial-payne-a3ecbd`, `readme-semantic-cache`) — all fully merged into `main` or
  superseded by this cleanup.
- Deleted the stray `gateway` build artifact from the repo root; added `/gateway` to
  `.gitignore`.
- Pruned this file — collapsed roughly 30 near-duplicate session-log entries that had
  repeated the same "remove stale worktrees" TODO across many sessions without acting
  on it.
- **Next:** scope the next feature (request logging, auth key management, or cost
  budget enforcement).
