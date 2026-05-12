package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

type MockProvider struct {
	response string
}

func NewMockProvider(response string) *MockProvider {
	return &MockProvider{response: response}
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Chat(_ context.Context, req *models.ChatRequest) (*models.ChatResponse, error) {
	words := strings.Fields(m.response)
	return &models.ChatResponse{
		ID:    "mock-chat-001",
		Model: req.Model,
		Choices: []models.Choice{
			{
				Index:        0,
				Message:      models.Message{Role: "assistant", Content: m.response},
				FinishReason: "stop",
			},
		},
		Usage: models.Usage{
			PromptTokens:     10,
			CompletionTokens: len(words),
			TotalTokens:      10 + len(words),
		},
	}, nil
}

func (m *MockProvider) ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error) {
	words := strings.Fields(m.response)
	ch := make(chan models.StreamEvent, len(words)+1)
	go func() {
		defer close(ch)
		for i, word := range words {
			select {
			case <-ctx.Done():
				return
			default:
			}
			delta := word
			if i < len(words)-1 {
				delta += " "
			}
			ch <- models.StreamEvent{ID: fmt.Sprintf("mock-%d", i), Delta: delta}
		}
		ch <- models.StreamEvent{Done: true}
	}()
	return ch, nil
}
