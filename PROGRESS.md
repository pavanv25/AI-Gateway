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

---

### Circuit Breaker & Provider Cooldown

- 3-state machine (Closed / Open / HalfOpen) per provider, in-memory, goroutine-safe.
- Trips on consecutive 5xx, 429, and network errors; context cancellation does not count.
- `CircuitBreaker` wraps every registered `Provider` transparently via `provider.New(p, cfg)`.
- `ErrCircuitOpen` is retriable — fallback loop skips to the next alias entry automatically.
- Configurable via `CB_FAILURE_THRESHOLD` and `CB_COOLDOWN_SECONDS`; opt-in (unset = disabled).

## Next Steps

- **Alias integration tests** — `MockProvider` covering entry-1 retriable
  failure → entry-2 fallback and non-retriable 4xx immediate break.
- **Streaming failover tests** — cover the `contentSent` guard in
  `handleStreamWithFallback`.
- **SemanticCache unit tests** — mock Qdrant HTTP server covering hit, miss,
  and store-failure paths.

---

## Session Log — 2026-06-01

- Condensed `PROGRESS.md` from ~194 lines to a tighter format; removed stale
  detail that duplicated `CLAUDE.md`.
- Updated `CLAUDE.md` with `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL`
  env var entries, closing out the docs next step.
- Cleaned up leftover worktrees (`cleanup-progress-md`, `readme-semantic-cache`,
  and others) after merging prior PRs.
- **Next:** write alias integration tests (`MockProvider` retriable fallback),
  streaming failover tests (`contentSent` guard), and SemanticCache unit tests
  (mock Qdrant HTTP server).

## Session Log — 2026-06-01 (end of session)

- Cleaned up stale worktrees (`cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`, `jovial-payne-a3ecbd`, `readme-semantic-cache`) left over from merged PRs.
- Condensed `PROGRESS.md` (~108 lines removed) and updated `CLAUDE.md` with `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL` env var docs; no code changes.
- **Next:** write alias integration tests (`MockProvider` retriable fallback → entry-2 advance, non-retriable 4xx break).
- **Next:** add streaming failover tests covering the `contentSent` guard in `handleStreamWithFallback`.
- **Next:** add `SemanticCache` unit tests with a mock Qdrant HTTP server (hit, miss, store-failure paths).


## 2026-06-01 16:46

- Cleaned up stale worktrees from merged PRs and tightened `PROGRESS.md` (~108 lines removed, no code changes).
- Updated `CLAUDE.md` with `QDRANT_URL`, `QDRANT_API_KEY`, `CACHE_TTL` env var docs.
- **Next:** alias integration tests (`MockProvider` retriable fallback → entry-2, non-retriable 4xx break), streaming failover tests (`contentSent` guard), and `SemanticCache` unit tests with a mock Qdrant HTTP server.

## 2026-06-01 (end of session)

- Reformatted `CLAUDE.md`: replaced free-form prose with structured Tech Stack list, expanded Project Layout to include `cache/` and missing `_test.go` files, added env var table with `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL`.
- Condensed `PROGRESS.md` by ~108 lines; removed stale detail already captured in `CLAUDE.md`.
- Removed five stale worktrees (`cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`, `jovial-payne-a3ecbd`, `readme-semantic-cache`) left over from merged PRs.
- **Next:** alias integration tests — `MockProvider` retriable fallback advancing to entry-2, non-retriable 4xx breaking immediately.
- **Next:** streaming failover tests (`contentSent` guard) and `SemanticCache` unit tests with a mock Qdrant HTTP server.


## 2026-06-01 16:54

PROGRESS.md updated with a 5-bullet session entry covering the `CLAUDE.md` restructure, `PROGRESS.md` condensation, worktree cleanup, and the three pending test gaps.

## 2026-06-01 (latest)

- Reformatted `CLAUDE.md` into structured Tech Stack + env var table; expanded Project Layout to include `cache/` and `_test.go` files.
- Condensed `PROGRESS.md` by ~108 lines, removing stale session noise that duplicated static docs.
- Removed five merged-PR worktrees (`cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`, `jovial-payne-a3ecbd`, `readme-semantic-cache`).
- **Next:** alias integration tests — `MockProvider` retriable fallback → entry-2 advance, non-retriable 4xx break.
- **Next:** streaming failover tests (`contentSent` guard) and `SemanticCache` unit tests with a mock Qdrant HTTP server.


## 2026-06-01 16:55

- Reformatted `CLAUDE.md` into structured Tech Stack + env var table; expanded Project Layout to include `cache/` and `_test.go` files.
- Condensed `PROGRESS.md` by ~108 lines, removing stale session noise that duplicated static docs.
- Removed five merged-PR worktrees (`cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`, `jovial-payne-a3ecbd`, `readme-semantic-cache`).
- **Next:** alias integration tests — `MockProvider` retriable fallback → entry-2 advance, non-retriable 4xx break.
- **Next:** streaming failover tests (`contentSent` guard) and `SemanticCache` unit tests with a mock Qdrant HTTP server.


## 2026-06-01 16:57

- Restructured `CLAUDE.md` with a Tech Stack list, expanded Project Layout (added `cache/`, `_test.go` entries), and an env var table covering `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL`.
- Condensed `PROGRESS.md` by ~108 lines, removing duplicated session noise already captured in static docs.
- Removed five stale worktrees (`cleanup-progress-md`, `effervescent-foraging-rabbit`, `eventual-plotting-flask`, `jovial-payne-a3ecbd`, `readme-semantic-cache`) left over from merged PRs.
- **Next:** alias integration tests — `MockProvider` retriable fallback advancing to entry-2, non-retriable 4xx breaking immediately.
- **Next:** streaming failover tests (`contentSent` guard) and `SemanticCache` unit tests with a mock Qdrant HTTP server (hit, miss, store-failure paths).


## 2026-06-01 17:52

- Instrumented `chatHandler` and fallback handlers with `metrics.Collector` — emits `MetricEvent` on every cache hit and provider response, capturing latency (`RequestLatencyMs`, `CacheLatencyMs`, `ProviderLatencyMs`), token counts, and stream flag.
- Added `NoopCollector{}` as a zero-value fallback in `RegisterRoutes` so a nil collector is safe without callers changing.
- Started `circuit-breaker` worktree — pattern suggests per-provider open/half-open/closed state to stop retrying persistently failing backends.
- **Next:** implement a `metrics.Exporter` (Prometheus or log-based) that consumes `MetricEvent` from `Collector`; wire into `cmd/gateway/main.go`.
- **Next:** finish circuit breaker in the open worktree and add alias integration tests covering `MockProvider` retriable fallback → entry-2 advance and non-retriable 4xx break.

