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
- `GET /metrics` also returns `KeyBreakdowns` (per-`APIKeyHash` aggregates, capped to the
  top 10 by `RequestCount` with the remainder rolled into an `"other"` entry — see
  `internal/metrics/store.go`'s `bucketSet.byKey` and `topNKeyBreakdowns`), `RateLimit`
  (`{Used, Limit}` — the *caller's own* current TPM usage via a new non-mutating
  `Limiter.Usage` read, `internal/ratelimit/limiter.go`), and `CircuitBreakers`
  (`[]{Provider, State}` via a new `CircuitBreaker.State()` getter, `"n/a"` when breakers
  are disabled). Frontend: `KeyBreakdownChart` (truncated 8-char hash labels) and
  `StatusPanel` (TPM usage bar + per-provider state badges) added to `dashboard/`.

### Request Logging

- `internal/reqlog`: `Middleware()` assigns a per-request ID (`crypto/rand`), sets it as the
  `X-Request-ID` response header, and emits one structured `slog` JSON line per request
  (`method`, `path`, `status`, `latency_ms`, `client_ip`, `api_key_hash`) after it completes.
  API keys are hashed (SHA-256), never logged in the clear; no prompt/response content is logged.
- `cmd/gateway/main.go`: `gin.Default()` replaced with `gin.New()` + `gin.Recovery()` +
  `reqlog.Middleware()`; `slog.SetDefault` set to a JSON handler on stdout, which also
  structures the existing `log.Printf` startup/config lines for free.
- `internal/api/routes.go`: request ID threaded through `resolveEntries`,
  `handleChatWithFallback`, and `handleStreamWithFallback`; all fallback-loop `log.Printf`
  calls (reserve errors, circuit-open skips, retry attempts) converted to `slog.Warn`/
  `slog.Error` carrying `request_id`, so every log line for one request can be correlated.

## Next Steps

Dashboard milestone (Steps 1–6), request logging, and the per-API-key breakdown /
rate-limit + circuit-breaker status panel dashboard additions are complete on `main`.
No feature is currently scoped — candidates for the next milestone: auth key management
or cost budget enforcement.

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

### 2026-07-13 — Request logging

- Added `internal/reqlog` (`Middleware()` + tests): per-request ID, `X-Request-ID` response
  header, one structured `slog` JSON access-log line per request, API key logged as a
  SHA-256 hash only, no prompt/response content logged.
- Wired into `cmd/gateway/main.go` (`gin.New()` + `Recovery` + `reqlog.Middleware()`,
  `slog.SetDefault` JSON handler) and `internal/api/routes.go` (request ID threaded through
  the fallback loop; all `log.Printf` calls there converted to structured `slog` calls).
- Verified manually against a running gateway + local Redis: `X-Request-ID` header matches
  the log line's `request_id`, and fallback-loop warnings correlate to the same request ID
  as the final access-log line. `go test ./... -race` green.
- **Next:** scope auth key management or cost budget enforcement.

### 2026-07-13 — Design docs & linting config

- Added `brainstorm.md` as a retroactive design reference capturing MVP scope, request flow, and early ideas on routing, failover, streaming, and rate limiting.
- Added `.markdownlint.json` enforcing a 120-char line limit with table and inline-HTML checks disabled; config only, not wired into any build or test path.
- **Next:** reconcile `brainstorm.md`'s fixed-window rate-limiting framing with the sliding-window TPM implementation in `internal/ratelimit/limiter.go`.
- **Next:** if cost/latency routing is revisited, use `brainstorm.md` as the starting point for a concrete provider-selection algorithm in `internal/alias/`.
- **Next:** commit both files so the brainstorm is tracked in git history alongside the implementation.

## 2026-07-13 11:37

PROGRESS.md updated — collapsed the six near-duplicate session entries into a single clean `### 2026-07-13 — Design docs & linting config` block with 5 bullets covering what was added and three concrete next steps.


## 2026-07-13 11:39

- Added `brainstorm.md` as a retroactive design reference: MVP scope, request-flow diagram, and early routing/failover/streaming/rate-limiting ideas that predate the current implementation.
- Added `.markdownlint.json` enforcing a 120-char line limit; table and inline-HTML checks disabled. Not wired into any build or test path — config only.
- Pruned `PROGRESS.md`: collapsed near-duplicate session-log entries (net −16 lines).
- **Next:** commit `brainstorm.md` and `.markdownlint.json` so both are tracked in git history.
- **Next:** reconcile `brainstorm.md`'s fixed-window rate-limiting framing with the sliding-window TPM implementation in `internal/ratelimit/limiter.go`.

### 2026-07-13 — Per-API-key breakdown + rate-limit/circuit-breaker status panel

- `internal/ratelimit/limiter.go`: added `Limiter.Usage(ctx, apiKey) (used, limit int, err error)`
  backed by a new non-mutating `peekLua` script (prunes expired entries, sums remaining
  reservations, no `ZADD`/`EXPIRE`) — safe to poll on every dashboard refresh.
- `internal/provider/circuit.go`: added `CircuitBreaker.State()` (locking getter) and
  `State.String()` (`"closed"`/`"open"`/`"half_open"`).
- `internal/metrics/store.go`: added a `byKey map[string]*bucket` dimension to `bucketSet`
  (parallel to the existing `byPM`), populated from `MetricEvent.APIKeyHash`. `Query()` now
  returns `Snapshot.KeyBreakdowns`, capped to the top 10 keys by `RequestCount` via
  `topNKeyBreakdowns`, with the remainder folded into a single `APIKeyHash: "other"` entry.
- `internal/api/routes.go`: `metricsHandler` now also takes `limiter` and `providers`;
  `GET /metrics` response is a `metricsResponse` wrapping `Snapshot` with `RateLimit`
  (caller's own usage) and `CircuitBreakers` (per-provider state, `"n/a"` when breakers
  are disabled — type-asserts each `provider.Provider` to `*provider.CircuitBreaker`).
- `dashboard/`: `KeyBreakdownChart` (clone of `BreakdownChart`, truncated 8-char hash
  labels) and `StatusPanel` (TPM usage bar + colored per-provider state badges) added;
  wired into `App.tsx` and `types.ts`.
- Verified end-to-end: gateway + local Redis, `CB_FAILURE_THRESHOLD=1`, requests from two
  distinct API keys — dashboard correctly split the key-breakdown chart, showed live TPM
  usage, and reported the mock provider's circuit as `closed`. `go test ./... -race` and
  `tsc --noEmit` both green.
- **Next:** scope auth key management or cost budget enforcement.


## 2026-07-13 12:00

Now I have enough context. The session added per-API-key metrics breakdown, a non-mutating TPM usage peek, circuit breaker state exposure, and two new dashboard components. Here are the bullet points:

- Added per-API-key aggregate metrics to `metrics.Store` (`bucketSet.byKey`, `topNKeyBreakdowns`): top-10 keys by request count, remainder rolled into an `"other"` entry.
- Added `Limiter.Usage()` — a non-mutating Redis Lua peek that returns the caller's current TPM consumption alongside the configured limit, without writing a reservation.
- Exposed `CircuitBreaker.State()` + `State.String()` and enriched `GET /metrics` to return `RateLimit` and `CircuitBreakers` fields alongside the existing `Snapshot`.
- Dashboard: added `KeyBreakdownChart` (8-char truncated hash labels) and `StatusPanel` (TPM usage bar + per-provider state badges) to `App.tsx`.
- Next: wire API-key management UI (create/revoke keys); add per-key cost budget enforcement with configurable hard/soft limits; extend `metricsStreamHandler` to push `RateLimit` and `CircuitBreakers` diffs over SSE.


## 2026-07-13 13:58

- Added per-API-key aggregate metrics to `metrics.Store` — parallel `byKey` dimension on `bucketSet`, `topNKeyBreakdowns` caps output to top-10 keys by request count with the remainder folded into an `"other"` entry.
- Added non-mutating `Limiter.Usage()` backed by a new `peekLua` script (prunes expired entries, sums reservations, no writes) — safe to call on every dashboard refresh.
- Exposed `CircuitBreaker.State()` + `State.String()` and enriched `GET /metrics` response with `RateLimit` (caller's own TPM usage) and `CircuitBreakers` (per-provider state, `"n/a"` when disabled).
- Dashboard: new `KeyBreakdownChart` (8-char truncated hash labels) and `StatusPanel` (TPM progress bar + colored circuit-state badges) components wired into `App.tsx`; matching types added to `types.ts`.
- **Next:** add API-key management endpoints (create/revoke); enforce per-key cost budget limits; push `RateLimit`/`CircuitBreakers` diffs over the existing SSE stream.


## 2026-07-13 14:01

- Added per-API-key aggregate metrics to `metrics.Store`: parallel `byKey` bucket dimension, `topNKeyBreakdowns` caps output to top-10 keys by request count with the remainder folded into an `"other"` entry.
- Added non-mutating `Limiter.Usage()` backed by a new `peekLua` Lua script (prunes expired entries, sums reservations, no writes) — safe to poll on every dashboard refresh.
- Exposed `CircuitBreaker.State()` + `State.String()` and enriched `GET /metrics` to return `RateLimit` (caller's TPM usage) and `CircuitBreakers` (per-provider state, `"n/a"` when disabled).
- Dashboard: new `KeyBreakdownChart` and `StatusPanel` (TPM progress bar + colored circuit-state badges) wired into `App.tsx`; matching types in `types.ts`.
- **Next:** add API-key management endpoints (create/revoke); enforce per-key cost budget limits; push `RateLimit`/`CircuitBreakers` diffs over the existing SSE stream.

