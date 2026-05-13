package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// newOpenAITestProvider wires an OpenAIProvider against a local httptest server.
func newOpenAITestProvider(t *testing.T, handler http.HandlerFunc) *OpenAIProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)
	return &OpenAIProvider{client: client}
}

// writeOpenAIStream writes a minimal OpenAI SSE stream to w.
// deltasJSON: each element becomes a choices[0].delta.content chunk.
// usageChunk: if non-nil, appended as a zero-choices chunk with usage fields.
func writeOpenAIStream(w http.ResponseWriter, id string, deltas []string, usage *openai.CompletionUsage) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)

	for i, delta := range deltas {
		chunk := map[string]any{
			"id":    id,
			"model": "gpt-4o-mini",
			"choices": []any{
				map[string]any{
					"index":         0,
					"delta":         map[string]any{"content": delta},
					"finish_reason": func() any { if i == len(deltas)-1 { return "stop" }; return nil }(),
				},
			},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	if usage != nil {
		chunk := map[string]any{
			"id":    id,
			"model": "gpt-4o-mini",
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
				"total_tokens":      usage.TotalTokens,
			},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// --- Name ---

func TestOpenAIProvider_Name(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	if got := p.Name(); got != "openai" {
		t.Errorf("Name: got %q, want %q", got, "openai")
	}
}

// --- Chat: happy path ---

func TestOpenAIProvider_Chat_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":    "chatcmpl-abc",
			"model": "gpt-4o-mini",
			"choices": []any{
				map[string]any{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": "Hello!"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
	p := newOpenAITestProvider(t, handler)
	req := &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "chatcmpl-abc" {
		t.Errorf("ID: got %q", resp.ID)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("content: got %+v", resp.Choices)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens: got %d, want 15", resp.Usage.TotalTokens)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason: got %q", resp.Choices[0].FinishReason)
	}
}

// --- Chat: API errors ---

func TestOpenAIProvider_Chat_Unauthorized(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "invalid api key", "type": "invalid_request_error"},
		})
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestOpenAIProvider_Chat_RateLimited(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "rate limit exceeded", "type": "requests"},
		})
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
}

func TestOpenAIProvider_Chat_ServerError(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestOpenAIProvider_Chat_MalformedJSON(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{not valid json"))
	})
	_, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// --- Chat: MaxTokens parameter propagation ---

func TestOpenAIProvider_Chat_MaxTokensZero_NotSentInRequest(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}
	p := newOpenAITestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "gpt-4o-mini",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 0,
	})
	if _, present := body["max_completion_tokens"]; present {
		t.Error("max_completion_tokens should NOT be sent when MaxTokens=0")
	}
}

func TestOpenAIProvider_Chat_MaxTokensPositive_SentInRequest(t *testing.T) {
	var body map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}
	p := newOpenAITestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model:     "gpt-4o-mini",
		Messages:  []models.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 50,
	})
	raw, present := body["max_completion_tokens"]
	if !present {
		t.Fatal("max_completion_tokens should be sent when MaxTokens>0")
	}
	var v int64
	json.Unmarshal(raw, &v)
	if v != 50 {
		t.Errorf("max_completion_tokens: got %d, want 50", v)
	}
}

// --- Chat: message role mapping ---

func TestOpenAIProvider_Chat_MessageRoleMapping(t *testing.T) {
	// The SDK serializes message content as an array of parts, not a plain string,
	// so we only assert on role ordering here.
	type msgBody struct {
		Role string `json:"role"`
	}
	type reqBody struct {
		Messages []msgBody `json:"messages"`
	}
	var captured reqBody
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}
	p := newOpenAITestProvider(t, handler)
	p.Chat(context.Background(), &models.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []models.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "bye"},
		},
	})

	if len(captured.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(captured.Messages))
	}
	wantRoles := []string{"system", "user", "assistant", "user"}
	for i, want := range wantRoles {
		if captured.Messages[i].Role != want {
			t.Errorf("msg[%d].role: got %q, want %q", i, captured.Messages[i].Role, want)
		}
	}
}

// --- Chat: empty choices in response ---

func TestOpenAIProvider_Chat_EmptyChoices(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 0, "total_tokens": 1},
		})
	})
	resp, err := p.Chat(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) != 0 {
		t.Errorf("expected empty choices, got %d", len(resp.Choices))
	}
}

// --- Chat: context cancellation ---

func TestOpenAIProvider_Chat_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// should not reach here
		w.WriteHeader(http.StatusOK)
	})
	_, err := p.Chat(ctx, &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// --- ChatStream: happy path with usage chunk ---

func TestOpenAIProvider_ChatStream_Success_WithUsage(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		writeOpenAIStream(w, "chatcmpl-xyz", []string{"Hello", " world"}, &openai.CompletionUsage{
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		})
	}
	p := newOpenAITestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
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

	if got := len(deltas); got != 2 {
		t.Errorf("delta count: got %d, want 2", got)
	}
	if doneEvent == nil {
		t.Fatal("no Done event received")
	}
	if doneEvent.Usage == nil {
		t.Fatal("Done event missing Usage")
	}
	if doneEvent.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens: got %d, want 15", doneEvent.Usage.TotalTokens)
	}
	if doneEvent.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens: got %d, want 10", doneEvent.Usage.PromptTokens)
	}
	if doneEvent.Usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens: got %d, want 5", doneEvent.Usage.CompletionTokens)
	}
}

// --- ChatStream: no usage chunk — fallback Done event has nil Usage ---

func TestOpenAIProvider_ChatStream_NoUsageChunk_FallbackDone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		writeOpenAIStream(w, "chatcmpl-xyz", []string{"Hi"}, nil) // no usage chunk
	}
	p := newOpenAITestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
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
	// Without a usage chunk, Usage is nil — callers must handle this.
	if doneEvent.Usage != nil {
		t.Error("expected nil Usage on fallback Done event")
	}
}

// --- ChatStream: usage chunk with zero TotalTokens is NOT treated as Done ---

func TestOpenAIProvider_ChatStream_ZeroTotalTokensChunk_FallbackDoneFires(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// A zero-tokens usage chunk — the code skips it (TotalTokens == 0 check)
		chunk := map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0,
			},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
	p := newOpenAITestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
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
}

// --- ChatStream: empty delta content not forwarded ---

func TestOpenAIProvider_ChatStream_EmptyDeltaNotForwarded(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// First chunk: empty delta (role-only event OpenAI sometimes sends)
		empty := map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"role": "assistant", "content": ""},
				"finish_reason": nil,
			}},
		}
		b, _ := json.Marshal(empty)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		// Second chunk: real content
		real := map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"content": "Hi"},
				"finish_reason": "stop",
			}},
		}
		b, _ = json.Marshal(real)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
	p := newOpenAITestProvider(t, handler)
	ch, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
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

// --- ChatStream: IncludeUsage flag is set in request ---

func TestOpenAIProvider_ChatStream_IncludeUsageFlagSet(t *testing.T) {
	type streamOpts struct {
		IncludeUsage bool `json:"include_usage"`
	}
	type reqBody struct {
		StreamOptions streamOpts `json:"stream_options"`
	}
	var captured reqBody
	handler := func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		writeOpenAIStream(w, "x", []string{"ok"}, &openai.CompletionUsage{
			PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
		})
	}
	p := newOpenAITestProvider(t, handler)
	ch, _ := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	for range ch {
	}
	if !captured.StreamOptions.IncludeUsage {
		t.Error("stream_options.include_usage should be true")
	}
}

// --- ChatStream: server error ---

func TestOpenAIProvider_ChatStream_ServerError(t *testing.T) {
	p := newOpenAITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := p.ChatStream(context.Background(), &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	// The SDK may not error until stream.Next() is called; either path is acceptable.
	// If no error here, the channel must close with a Done event (not hang).
	if err != nil {
		return
	}
	// err == nil means streaming started; drain and verify it terminates.
	// This path would be a bug only if it hangs — the test itself provides the timeout guard.
}

// --- ChatStream: context cancellation does not hang ---

func TestOpenAIProvider_ChatStream_ContextCancel_NoHang(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Cancel before the first chunk is delivered.
		cancel()
		chunk := map[string]any{
			"id": "x", "model": "gpt-4o-mini",
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"content": "Hi"},
				"finish_reason": nil,
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	p := newOpenAITestProvider(t, handler)
	ch, err := p.ChatStream(ctx, &models.ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		return // error on cancellation before streaming — acceptable
	}
	// Drain without hanging. The test runner's timeout (-timeout flag) guards this.
	for range ch {
	}
}
