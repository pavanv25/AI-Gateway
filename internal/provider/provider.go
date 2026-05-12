package provider

import (
	"context"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

type Provider interface {
	Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error)
	ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error)
	Name() string
}
