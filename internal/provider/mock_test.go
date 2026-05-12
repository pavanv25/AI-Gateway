package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

func TestMockProvider_Chat(t *testing.T) {
	const response = "Hello, world!"
	p := provider.NewMockProvider(response)

	req := &models.ChatRequest{
		Model:    "mock-model",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
	}

	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if got := resp.Choices[0].Message.Content; got != response {
		t.Errorf("content: got %q, want %q", got, response)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
}

func TestMockProvider_ChatStream(t *testing.T) {
	const response = "Hello world"
	p := provider.NewMockProvider(response)

	req := &models.ChatRequest{
		Model:    "mock-model",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	}

	ch, err := p.ChatStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var buf strings.Builder
	for event := range ch {
		if event.Done {
			break
		}
		buf.WriteString(event.Delta)
	}

	if got := buf.String(); got != response {
		t.Errorf("got %q, want %q", got, response)
	}
}

func TestMockProvider_ChatStream_ContextCancel(t *testing.T) {
	p := provider.NewMockProvider("a b c d e f g h i j")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &models.ChatRequest{
		Model:    "mock-model",
		Messages: []models.Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	}

	ch, err := p.ChatStream(ctx, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// draining must complete without hanging
	for range ch {
	}
}

func TestMockProvider_Name(t *testing.T) {
	if got := provider.NewMockProvider("").Name(); got != "mock" {
		t.Errorf("Name: got %q, want %q", got, "mock")
	}
}
