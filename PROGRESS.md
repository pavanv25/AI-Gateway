# PROGRESS.md

Active development state for the AI Gateway. See CLAUDE.md for static technical context.

---

## Current Focus

Task-Based Aliasing is shipped. The next priorities are integration tests for the alias fallback path and README documentation for the new `task` field and `ALIAS_CONFIG` env var.

## Session — 2026-05-26

- Expanded `CLAUDE.md` with env var documentation (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `ALIAS_CONFIG`), full request lifecycle walkthrough, and updated project layout and scaffolding rules.
- Opened a second worktree (`eventual-plotting-flask`) for isolated branch experimentation alongside the existing `jovial-payne-a3ecbd` worktree.
- Extended `.claude/settings.local.json` with additional `Bash` and tool permission allowances to reduce confirmation prompts during build and test workflows.
- **Next:** write alias integration tests using `MockProvider` — cover entry-1 retriable failure → entry-2 fallback and non-retriable 4xx immediate break.
- **Next:** document `task` field, `ALIAS_CONFIG`, and `config/aliases.example.yaml` in README; add streaming failover test coverage.

---

## Session — 2026-05-23

- Implemented `internal/alias` package: `Resolver` loads a YAML config at startup and maps task names to ordered `[]Entry{provider, model}` lists; `alias_test.go` covers resolve, missing-task, and disabled-resolver cases.
- Wired alias fallback loop into `internal/api/routes.go`: `resolveEntries` dispatches to alias or direct provider, `handleChatWithFallback` and `handleStreamWithFallback` iterate entries and retry on retriable errors, attaching `resolved_provider` to the response.
- Added `provider.ProviderError{StatusCode, Cause}` and `IsRetriable` to `provider.go`; OpenAI and Anthropic providers now wrap SDK errors into `*ProviderError` so the handler classifies retriability without importing SDK types.
- **Next:** write alias integration tests using `MockProvider` to cover entry-1 failure → entry-2 fallback and non-retriable (4xx) immediate break.
- **Next:** document `task` field, `ALIAS_CONFIG`, and `config/aliases.example.yaml` in README; add streaming failover test coverage.

---

## Recent Accomplishments

### Task-Based Aliasing
Callers can now send `{"task": "fast-chat", ...}` instead of hardcoding a provider and model. The gateway resolves the task name to an ordered `[{provider, model}]` fallback list defined in a YAML config file loaded via `ALIAS_CONFIG` at startup. Automatic failover fires on 5xx or 429 from the upstream provider; 4xx and context cancellation fail immediately. For streaming, failover is only possible before the first content chunk is flushed to the client. The response includes a `resolved_provider` field showing which backend handled the request.

Key files: `internal/alias/alias.go`, `internal/api/routes.go`, `cmd/gateway/main.go`, `config/aliases.example.yaml`.

### Provider error classification
Added `provider.ProviderError{StatusCode, Cause}` and `provider.IsRetriable` to `internal/provider/provider.go`. Both OpenAI and Anthropic providers now wrap SDK errors into `*ProviderError` so the handler can classify retriability without importing SDK types. Stream goroutines emit `StreamEvent{Done:true, Err:...}` on `stream.Err()` instead of silently closing the channel.

### OpenAI and Anthropic providers
Full `Chat` + `ChatStream` implementations using their official Go SDKs. Anthropic extracts system messages into the top-level `system` field. OpenAI streaming sets `IncludeUsage: true` to capture a final token-count chunk. Both providers are registered conditionally on API key presence at startup.

### Rate limiting and streaming token accounting
Sliding 60-second window keyed on `X-API-Key` via Redis sorted set + Lua scripts. `Reserve` atomically checks capacity; `Commit` corrects to actual usage after each response. Streaming reads `event.Usage.TotalTokens` from the terminal `Done` event and passes it to `Commit`.

---

## Session — 2026-05-26 (later)

- Added `internal/cache` package: `Cache` interface + `SemanticCache` implementation using Qdrant (REST) for vector storage and OpenAI `text-embedding-3-small` for prompt embeddings; `AsyncStore` runs writes on a bounded background goroutine pool.
- Wired semantic cache into `cmd/gateway/main.go` (enabled when `QDRANT_URL` + `OPENAI_API_KEY` are set) and `internal/api/routes.go` (lookup before rate-limit check; hit returns `resolved_provider: "cache"` and still debits the token budget).
- Extended `pkg/models/models.go` with `CacheHit bool` on `ChatResponse`; `routes_test.go` added.
- **Next:** add `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL` to `CLAUDE.md` env var table and README.
- **Next:** write unit tests for `SemanticCache` using a mock HTTP server; cover hit, miss, and store-failure paths.

---

### Circuit Breaker & Provider Cooldown

- 3-state machine (Closed / Open / HalfOpen) per provider, in-memory, goroutine-safe.
- Trips on consecutive 5xx, 429, and network errors; context cancellation does not count.
- `CircuitBreaker` wraps every registered `Provider` transparently via `provider.New(p, cfg)`.
- `ErrCircuitOpen` is retriable — fallback loop skips to the next alias entry automatically.
- Configurable via `CB_FAILURE_THRESHOLD` and `CB_COOLDOWN_SECONDS`; opt-in (unset = disabled).

## Next Steps

- **Alias integration tests** — test that a retriable error on entry 1 falls through to entry 2, and a non-retriable error (400/401) breaks immediately. Wire `MockProvider` so tests run without live API keys.
- **README docs** — document the `task` field, `ALIAS_CONFIG` format, and the `config/aliases.example.yaml` file.
- **Streaming failover tests** — extend mock to simulate errors at configurable points so the streaming fallback path is covered by tests.

## 2026-05-23 15:45

- Implemented `internal/alias` package with YAML-based task-to-provider resolver
- Wired chat and streaming fallback loops into `routes.go` with retriable error classification
- Added `provider.ProviderError` + `IsRetriable` so OpenAI/Anthropic wrap SDK errors uniformly
- Next: alias integration tests with `MockProvider`, then README docs for the `task` field and `ALIAS_CONFIG`


## 2026-05-23 15:47

- Captured project-level brainstorm (`brainstorm.md`) documenting MVP scope, high-level request flow, and routing/failover/streaming/rate-limiting design notes.
- Extended `.claude/settings.local.json` with additional `Bash` and `WebFetch` permission allowances to streamline future build, test, and fetch operations.
- Created a git worktree (`jovial-payne-a3ecbd`) for isolated branch experimentation; no changes were committed this session.
- **Next:** write alias integration tests using `MockProvider` covering entry-1 retriable failure → entry-2 fallback and non-retriable 4xx immediate break.
- **Next:** document `task` field, `ALIAS_CONFIG`, and `config/aliases.example.yaml` in README; add streaming failover test coverage.


## 2026-05-26 12:19

- Updated Claude Code project settings (`.claude/settings.local.json`) to configure allowed commands or hooks for this repo.
- Created and explored a git worktree (`jovial-payne-a3ecbd`) for isolated development; no code changes merged back yet.
- Expanded `brainstorm.md` with 9 additional lines — likely new routing, failover, or rate-limiting ideas building on the MVP scope.
- **Next:** write alias integration tests using `MockProvider` covering entry-1 failure → entry-2 fallback and non-retriable 4xx break.
- **Next:** document the `task` field, `ALIAS_CONFIG` env var, and `config/aliases.example.yaml` in README; add streaming failover test coverage.


## 2026-05-26 12:25

- Updated `CLAUDE.md` with expanded layout, full request lifecycle documentation, and environment variable entries for `ALIAS_CONFIG`, `OPENAI_API_KEY`, and `ANTHROPIC_API_KEY` — static context is now accurate and complete.
- Session tooling configured via `.claude/settings.local.json` (worktree and hook settings for the current project).
- **Next:** write alias integration tests using `MockProvider` covering entry-1 failure → entry-2 fallback and non-retriable 4xx immediate break.
- **Next:** add streaming failover test coverage (`contentSent` path in `handleStreamWithFallback`).
- **Next:** document `task` field, `ALIAS_CONFIG`, and `config/aliases.example.yaml` in README.


## 2026-05-26 (latest)

- Refreshed `CLAUDE.md` static docs: added request lifecycle section, corrected project layout, and documented all three env vars (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `ALIAS_CONFIG`).
- Updated `PROGRESS.md` to reflect completed alias/fallback work and carry forward outstanding next steps.
- **Next:** write alias integration tests — `MockProvider` covering entry-1 retriable failure → entry-2 fallback and non-retriable 4xx immediate break.
- **Next:** add streaming failover test coverage for the `contentSent` guard in `handleStreamWithFallback`.
- **Next:** document `task` field, `ALIAS_CONFIG`, and `config/aliases.example.yaml` in README.


## 2026-05-26 13:05

PROGRESS.md updated with a new session entry covering the `CLAUDE.md` docs refresh and carrying forward the three outstanding next steps (alias integration tests, streaming failover tests, README docs).


## 2026-05-26 14:16

- Updated `CLAUDE.md` with env vars, request lifecycle, and project layout details
- Opened new `eventual-plotting-flask` worktree for isolated branch work
- Extended `.claude/settings.local.json` permissions to reduce build/test prompts
- **Next:** alias integration tests with `MockProvider` (retriable fallback + non-retriable 4xx break)
- **Next:** README docs for `task` field, `ALIAS_CONFIG`, and streaming failover test coverage


## 2026-05-26 14:29

PROGRESS.md updated with a 5-bullet session entry covering the semantic cache implementation and next steps.


## 2026-05-26 (session end)

- Shipped `internal/cache` package: `Cache` interface + `SemanticCache` backed by Qdrant (REST) and OpenAI `text-embedding-3-small`; `AsyncStore` uses a bounded semaphore to avoid blocking the hot path.
- Wired semantic cache into `cmd/gateway/main.go` (initialised when `QDRANT_URL` + `OPENAI_API_KEY` are present) and `internal/api/routes.go` (lookup before rate-limit check; hits return `resolved_provider: "cache"` and still debit the token budget).
- Extended `pkg/models/models.go` with `CacheHit bool` on `ChatResponse`; added `routes_test.go` for handler-level coverage.
- **Next:** add `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL` to `CLAUDE.md` env var table and README.
- **Next:** write `SemanticCache` unit tests using a mock HTTP server covering hit, miss, and store-failure paths.


## 2026-05-26 14:37

- Added `internal/cache` package (`Cache` interface + `SemanticCache` via Qdrant + OpenAI embeddings), wired into the gateway with lazy initialisation on `QDRANT_URL`.
- Extended `ChatResponse` with `CacheHit bool`; handler now checks cache before rate-limit and returns `resolved_provider: "cache"` on hits.
- Next: document `QDRANT_URL`/`QDRANT_API_KEY`/`CACHE_TTL` in `CLAUDE.md` and README, then write `SemanticCache` unit tests with a mock HTTP server.


## 2026-05-26 14:46

Here are the bullet points for the new PROGRESS.md section:

- Added `internal/cache` package: `SemanticCache` implements a `Cache` interface backed by Qdrant (REST) for vector storage and OpenAI `text-embedding-3-small` for prompt embeddings; similarity threshold 0.95 with per-key-hash namespacing and TTL-controlled expiry.
- Wired semantic cache into `cmd/gateway/main.go` and `internal/api/routes.go`: cache lookup runs before the provider fallback loop; `AsyncStore` writes hits back on the hot path via a bounded 64-slot goroutine semaphore.
- Extended `pkg/models/models.go` with a `CacheHit` flag on `ChatResponse`; added `semantic_test.go` covering embed, store, lookup-hit, and lookup-miss paths against a mock Qdrant server.
- **Next:** add `QDRANT_URL`, `QDRANT_API_KEY`, and cache TTL env vars to `CLAUDE.md` and README; document `cache_hit` in the response schema.
- **Next:** write integration test for the full cache → provider fallback path; confirm streaming responses bypass cache correctly.


## 2026-05-26 14:50

- Shipped `internal/cache` package: `Cache` interface + `SemanticCache` backed by Qdrant (REST) and OpenAI `text-embedding-3-small` embeddings; `AsyncStore` uses a bounded goroutine semaphore for non-blocking hot-path writes.
- Wired cache into `cmd/gateway/main.go` (lazy init on `QDRANT_URL`) and `internal/api/routes.go` (lookup before rate-limit check; hits short-circuit provider fallback and return `resolved_provider: "cache"`).
- Extended `pkg/models/models.go` with `CacheHit bool` on `ChatResponse`; added `routes_test.go` for handler-level coverage and `semantic_test.go` with mock Qdrant server covering hit, miss, and embed paths.
- **Next:** document `QDRANT_URL`, `QDRANT_API_KEY`, and `CACHE_TTL` env vars in `CLAUDE.md` and README; add `cache_hit` to response schema docs.
- **Next:** write integration test for the full cache → provider fallback path; confirm streaming responses bypass the cache lookup correctly.

