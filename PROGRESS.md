# PROGRESS.md

Active development state for the AI Gateway. See CLAUDE.md for static technical context.

---

## Current Focus

The core gateway is feature-complete for single-provider routing with rate limiting. The next area of work is provider selection logic — right now the client must specify `"provider"` explicitly in every request. Making that smarter (fallback ordering, health-based selection) is the natural next step.

---

## Recent Accomplishments

### OpenAI and Anthropic providers implemented
`internal/provider/openai.go` and `internal/provider/anthropic.go` are fully wired using their official Go SDKs. Both support blocking `Chat` and streaming `ChatStream`. The Anthropic provider handles the API's structural requirement that system messages be extracted from the conversation array into a top-level `system` field (`buildAnthropicMessages`). The OpenAI streaming path sets `IncludeUsage: true` to receive a final token-count chunk.

### Streaming token accounting fixed
`StreamEvent` gained a `*Usage` field. The `handleStream` path in `routes.go` now reads `event.Usage.TotalTokens` from the terminal `Done` event and passes it to `Commit`, replacing the prior placeholder that always committed 0 tokens.

### `ChatRequest` extended
Added `Provider string` (selects backend by name) and `Temperature *float64` (propagated to both provider SDKs). The handler does a direct map lookup on `req.Provider`; unknown providers return 400.

### Provider registration made conditional
`main.go` registers OpenAI and Anthropic only when their API key env vars are present (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`). Missing keys log a warning; the gateway still starts with whatever providers are available.

### Full provider test suites added
- `openai_test.go` — 16 tests covering happy path, 4 HTTP error cases, MaxTokens propagation, role mapping, empty choices, context cancellation, streaming with/without usage chunk, empty delta filtering, and `IncludeUsage` flag verification.
- `anthropic_test.go` — 17 tests covering the same error surface plus Anthropic-specific cases: system message extraction, multiple system messages joined with `\n`, non-text content blocks (e.g. `tool_use`) silently ignored, and fallback `Done` event when `MessageDelta` is absent.

---

## Next Steps

- **Provider selection logic** — the client currently must name a provider explicitly. Consider a fallback ordering or a default when `provider` is omitted.
- **Streaming `MockProvider` usage** — `mock.go` now emits `Usage` on the `Done` event; verify end-to-end streaming tests cover the commit path with the mock.
- **Update project layout in CLAUDE.md** — the file tree still omits `openai.go`, `anthropic.go`, and their test files.
