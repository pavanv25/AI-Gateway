package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// newAnthropicTestProvider wires an AnthropicProvider against a local httptest server.
func newAnthropicTestProvider(t *testing.T, handler http.HandlerFunc) *AnthropicProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)
	return &AnthropicProvider{client: &client}
}

// writeAnthropicStream writes Anthropic SSE events to w.
// It models the real event sequence: message_start → content_block_delta(s) → message_delta.
func writeAnthropicStream(
	w http.ResponseWriter,
	msgID string,
	inputTokens int,
	deltas []string,
	outputTokens int,
) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)

	writeEvent := func(name string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		flusher.Flush()
	}

	writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":   msgID,
			"type": "message",
			"role": "assistant",
			"usage": map[string]any{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	})

	writeEvent("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})

	for _, delta := range deltas {
		writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": delta},
		})
	}

	writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})

	writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn"},
		"usage": map[string]any{"output_tokens": outputTokens},
	})

	writeEvent("message_stop", map[string]any{"type": "message_stop"})
}

// anthropicChatResponse builds a minimal non-streaming Anthropic response body.
func anthropicChatResponse(id string, text string, inputTok, outputTok int) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "message",
		"role": "assistant",
		"model": "claude-haiku-4-5-20251001",
		"content": []any{
			map[string]any{"type": "text", "text": text},
		},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens":  inputTok,
			"output_tokens": outputTok,
		},
	}
}

// --- Name ---

func TestAnthropicProvider_Name(t *testing.T) {
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	if got := p.Name(); got != "anthropic" {
		t.Errorf("Name: got %q, want %q", got, "anthropic")
	}
}

// --- Chat: happy path ---

func TestAnthropicProvider_Chat_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("msg-abc", "Hello!", 10, 5))
	}
	p := newAnthropicTestProvider(t, handler)
	resp, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "msg-abc" {
		t.Errorf("ID: got %q", resp.ID)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("content: got %+v", resp.Choices)
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens: got %d, want 10", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens: got %d, want 5", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens: got %d, want 15", resp.Usage.TotalTokens)
	}
}

// --- Chat: MaxTokens defaulting ---

func TestAnthropicProvider_Chat_MaxTokensZero_DefaultsTo1024(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 0,
	})
	raw, ok := body["max_tokens"]
	if !ok {
		t.Fatal("max_tokens must always be present in Anthropic request")
	}
	var v int64
	json.Unmarshal(raw, &v)
	if v != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens: got %d, want %d", v, anthropicDefaultMaxTokens)
	}
}

func TestAnthropicProvider_Chat_MaxTokensNegative_DefaultsTo1024(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: -5,
	})
	raw, ok := body["max_tokens"]
	if !ok {
		t.Fatal("max_tokens must always be present in Anthropic request")
	}
	var v int64
	json.Unmarshal(raw, &v)
	if v != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens: got %d, want %d", v, anthropicDefaultMaxTokens)
	}
}

func TestAnthropicProvider_Chat_MaxTokensPositive_UsedAsIs(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 256,
	})
	var v int64
	json.Unmarshal(body["max_tokens"], &v)
	if v != 256 {
		t.Errorf("max_tokens: got %d, want 256", v)
	}
}

// --- Chat: system message extraction ---

func TestAnthropicProvider_Chat_SystemMessage_ExtractedToTopLevel(t *testing.T) {
	type reqBody struct {
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
	}
	var captured reqBody
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model: "claude-haiku-4-5-20251001",
		Messages: []models.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
		},
		MaxTokens: 100,
	})

	// system field must be present
	if captured.System == nil {
		t.Fatal("system field missing from request")
	}
	// messages must NOT contain the system turn
	if len(captured.Messages) != 1 {
		t.Errorf("messages length: got %d, want 1 (system must be excluded)", len(captured.Messages))
	}
}

func TestAnthropicProvider_Chat_NoSystemMessage_SystemFieldAbsent(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hello"}},
		MaxTokens: 100,
	})
	if raw, ok := body["system"]; ok {
		// null or empty is acceptable; a non-null non-empty value is the bug.
		var v any
		json.Unmarshal(raw, &v)
		switch v := v.(type) {
		case nil:
			// ok
		case []any:
			if len(v) > 0 {
				t.Errorf("system field should be absent/empty when no system message, got %v", v)
			}
		case string:
			if v != "" {
				t.Errorf("system field should be absent/empty, got %q", v)
			}
		}
	}
}

func TestAnthropicProvider_Chat_MultipleSystemMessages_JoinedWithNewline(t *testing.T) {
	// buildAnthropicMessages joins multiple system messages with "\n".
	// Verify the helper directly via Chat by inspecting the request body.
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	var systemBlocks []textBlock
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System []textBlock `json:"system"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		systemBlocks = body.System
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicChatResponse("x", "ok", 1, 1))
	}
	p := newAnthropicTestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model: "claude-haiku-4-5-20251001",
		Messages: []models.Message{
			{Role: "system", Content: "rule one"},
			{Role: "system", Content: "rule two"},
			{Role: "user", Content: "go"},
		},
		MaxTokens: 100,
	})

	if len(systemBlocks) == 0 {
		t.Fatal("no system blocks in request")
	}
	combined := systemBlocks[0].Text
	if !strings.Contains(combined, "rule one") || !strings.Contains(combined, "rule two") {
		t.Errorf("system text should contain both messages, got %q", combined)
	}
}

// --- Chat: non-text content blocks ignored ---

func TestAnthropicProvider_Chat_NonTextContentBlocks_Ignored(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Mix a tool_use block with a text block — only text should appear in output.
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg-xyz",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-haiku-4-5-20251001",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "tool-1", "name": "calc", "input": map[string]any{}},
				map[string]any{"type": "text", "text": "Result is 4."},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 3},
		})
	}
	p := newAnthropicTestProvider(t, handler)
	resp, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "calc 2+2"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	content := resp.Choices[0].Message.Content
	if content != "Result is 4." {
		t.Errorf("content: got %q, want %q", content, "Result is 4.")
	}
}

// --- Chat: API errors ---

func TestAnthropicProvider_Chat_Unauthorized(t *testing.T) {
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error":   map[string]any{"type": "authentication_error", "message": "invalid api key"},
		})
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestAnthropicProvider_Chat_RateLimited(t *testing.T) {
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "rate_limit_error", "message": "rate limit hit"},
		})
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
}

func TestAnthropicProvider_Chat_ServerError(t *testing.T) {
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestAnthropicProvider_Chat_MalformedJSON(t *testing.T) {
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{not json"))
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// --- Chat: context cancellation ---

func TestAnthropicProvider_Chat_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := newAnthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, err := p.Chat(ctx, &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// --- ChatStream: happy path with full event sequence ---

func TestAnthropicProvider_ChatStream_Success_UsageAccumulated(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicStream(w, "msg-abc", 10, []string{"Hello", " world"}, 5)
	}
	p := newAnthropicTestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var deltas []string
	var doneEvent *models.StreamEvent
	for ev := range ch {
		if ev.Done {
			cp := ev
			doneEvent = &cp
			break
		}
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
	}

	if len(deltas) != 2 {
		t.Errorf("delta count: got %d, want 2", len(deltas))
	}
	if doneEvent == nil {
		t.Fatal("no Done event received")
	}
	if doneEvent.Usage == nil {
		t.Fatal("Done event missing Usage")
	}
	if doneEvent.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens: got %d, want 10", doneEvent.Usage.PromptTokens)
	}
	if doneEvent.Usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens: got %d, want 5", doneEvent.Usage.CompletionTokens)
	}
	if doneEvent.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens: got %d, want 15", doneEvent.Usage.TotalTokens)
	}
}

// --- ChatStream: missing MessageDeltaEvent — fallback Done fires with nil Usage ---

func TestAnthropicProvider_ChatStream_NoMessageDelta_FallbackDone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeEvent := func(name string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
			flusher.Flush()
		}
		// message_start and content, but NO message_delta
		writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg-x", "type": "message", "role": "assistant",
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 0},
			},
		})
		writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hi"},
		})
		writeEvent("message_stop", map[string]any{"type": "message_stop"})
	}
	p := newAnthropicTestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var doneEvent *models.StreamEvent
	for ev := range ch {
		if ev.Done {
			cp := ev
			doneEvent = &cp
			break
		}
	}
	if doneEvent == nil {
		t.Fatal("stream must always terminate with a Done event")
	}
	// fallback fires — Usage will be nil (no MessageDelta was received)
	if doneEvent.Usage != nil {
		t.Error("expected nil Usage when MessageDelta was never received")
	}
}

// --- ChatStream: missing MessageStartEvent — inputTokens stays 0 ---

func TestAnthropicProvider_ChatStream_NoMessageStart_InputTokensZero(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeEvent := func(name string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
			flusher.Flush()
		}
		// Skip message_start entirely
		writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hi"},
		})
		writeEvent("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"output_tokens": 3},
		})
	}
	p := newAnthropicTestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var doneEvent *models.StreamEvent
	for ev := range ch {
		if ev.Done {
			cp := ev
			doneEvent = &cp
			break
		}
	}
	if doneEvent == nil {
		t.Fatal("no Done event received")
	}
	if doneEvent.Usage == nil {
		t.Fatal("Done event should carry Usage from MessageDelta")
	}
	if doneEvent.Usage.PromptTokens != 0 {
		t.Errorf("PromptTokens: got %d, want 0 (no MessageStart received)", doneEvent.Usage.PromptTokens)
	}
	if doneEvent.Usage.CompletionTokens != 3 {
		t.Errorf("CompletionTokens: got %d, want 3", doneEvent.Usage.CompletionTokens)
	}
}

// --- ChatStream: empty text delta not forwarded ---

func TestAnthropicProvider_ChatStream_EmptyTextDelta_NotForwarded(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeEvent := func(name string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
			flusher.Flush()
		}
		writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg-x", "type": "message", "role": "assistant",
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 0},
			},
		})
		// Empty delta first
		writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": ""},
		})
		// Real delta
		writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hi"},
		})
		writeEvent("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"output_tokens": 1},
		})
	}
	p := newAnthropicTestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var deltas []string
	for ev := range ch {
		if ev.Done {
			break
		}
		deltas = append(deltas, ev.Delta)
	}
	if len(deltas) != 1 || deltas[0] != "Hi" {
		t.Errorf("deltas: got %v, want [\"Hi\"]", deltas)
	}
}

// --- ChatStream: context cancellation does not hang ---

func TestAnthropicProvider_ChatStream_ContextCancel_NoHang(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		cancel() // cancel before writing anything
		b, _ := json.Marshal(map[string]any{"type": "message_stop"})
		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", b)
		flusher.Flush()
	}

	p := newAnthropicTestProvider(t, handler)
	ch, err := p.ChatStream(ctx, &models.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		return
	}
	for range ch {
	}
}

// --- buildAnthropicMessages: unit tests ---

func TestBuildAnthropicMessages_RoleMapping(t *testing.T) {
	msgs := []models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "bye"},
	}
	params, systemText := buildAnthropicMessages(msgs)

	if systemText != "sys" {
		t.Errorf("systemText: got %q, want %q", systemText, "sys")
	}
	// params should contain only user and assistant messages
	if len(params) != 3 {
		t.Errorf("params length: got %d, want 3", len(params))
	}
}

func TestBuildAnthropicMessages_MultipleSystemMessages(t *testing.T) {
	msgs := []models.Message{
		{Role: "system", Content: "part one"},
		{Role: "system", Content: "part two"},
		{Role: "user", Content: "go"},
	}
	params, systemText := buildAnthropicMessages(msgs)

	if !strings.Contains(systemText, "part one") || !strings.Contains(systemText, "part two") {
		t.Errorf("systemText should contain both parts, got %q", systemText)
	}
	if len(params) != 1 {
		t.Errorf("params length: got %d, want 1 (only user message)", len(params))
	}
}

func TestBuildAnthropicMessages_NoSystemMessage(t *testing.T) {
	msgs := []models.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	params, systemText := buildAnthropicMessages(msgs)

	if systemText != "" {
		t.Errorf("systemText should be empty, got %q", systemText)
	}
	if len(params) != 2 {
		t.Errorf("params length: got %d, want 2", len(params))
	}
}
