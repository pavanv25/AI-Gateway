# PROGRESS.md

Active development state for the AI Gateway. See CLAUDE.md for static technical context.

---

## Current Focus

Task-Based Aliasing is shipped. The next priorities are integration tests for the alias fallback path and README documentation for the new `task` field and `ALIAS_CONFIG` env var.

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

## Next Steps

- **Alias integration tests** — test that a retriable error on entry 1 falls through to entry 2, and a non-retriable error (400/401) breaks immediately. Wire `MockProvider` so tests run without live API keys.
- **README docs** — document the `task` field, `ALIAS_CONFIG` format, and the `config/aliases.example.yaml` file.
- **Streaming failover tests** — extend mock to simulate errors at configurable points so the streaming fallback path is covered by tests.

## 2026-05-23 15:45

- Implemented `internal/alias` package with YAML-based task-to-provider resolver
- Wired chat and streaming fallback loops into `routes.go` with retriable error classification
- Added `provider.ProviderError` + `IsRetriable` so OpenAI/Anthropic wrap SDK errors uniformly
- Next: alias integration tests with `MockProvider`, then README docs for the `task` field and `ALIAS_CONFIG`

