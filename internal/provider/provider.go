package provider

import (
	"context"
	"errors"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

type Provider interface {
	Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error)
	ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error)
	Name() string
}

// ProviderError wraps an upstream HTTP error with its status code so the
// handler can decide whether to retry without importing SDK-specific types.
type ProviderError struct {
	StatusCode int
	Cause      error
}

func (e *ProviderError) Error() string { return e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }

// IsRetriable reports whether err should trigger failover to the next entry
// in a task's fallback list. Returns true for HTTP 429, any 5xx, or
// network-level errors. Returns false for context cancellation and other 4xx.
func IsRetriable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == 429 || pe.StatusCode >= 500
	}
	// Unknown error type (network failure before any HTTP response) — retriable.
	return true
}
