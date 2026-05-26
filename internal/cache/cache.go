package cache

import (
	"context"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

// Cache is the interface for semantic cache lookup and storage.
// A nil Cache disables caching — all call sites must guard with != nil.
// Both methods return (nil, nil, nil) / nil on any internal error; errors
// are logged but never propagated to callers.
type Cache interface {
	// Lookup returns the cached response and the computed embedding vector.
	// The vector is returned even on a miss so the caller can reuse it for
	// Store without a second embedding round-trip.
	// Returns (nil, nil, nil) on miss or any internal error.
	Lookup(ctx context.Context, key, apiKeyHash string) (*models.ChatResponse, []float64, error)

	// Store upserts the response keyed by key under apiKeyHash's namespace.
	// Accepts a pre-computed vector to avoid re-embedding on cache miss.
	// Best-effort: always returns nil; logs failures internally.
	Store(ctx context.Context, key, apiKeyHash string, vector []float64, resp *models.ChatResponse) error

	// AsyncStore launches Store in a bounded background goroutine using
	// context.Background(). It is a no-op when the goroutine pool is full,
	// logging the skip. Use this on the hot path after sending the response.
	AsyncStore(key, apiKeyHash string, vector []float64, resp *models.ChatResponse)
}
