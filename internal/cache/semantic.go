package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

const (
	collectionName = "semantic_cache"
	vectorDims     = 1536
	storeSemCap    = 64
)

// SemanticCache implements Cache using Qdrant (REST) for vector storage and
// OpenAI text-embedding-3-small for prompt embeddings.
type SemanticCache struct {
	openaiClient *openai.Client
	qdrantURL    string
	qdrantAPIKey string
	httpClient   *http.Client
	threshold    float64
	ttl          int64
	storeSem     chan struct{}
}

// New creates a SemanticCache and ensures the Qdrant collection and payload
// indexes exist. Returns an error if collection initialisation fails so that
// main.go can disable the cache rather than serving from a broken state.
func New(openAIAPIKey, qdrantURL, qdrantAPIKey string, ttlSeconds int64) (*SemanticCache, error) {
	s := &SemanticCache{
		openaiClient: openai.NewClient(option.WithAPIKey(openAIAPIKey)),
		qdrantURL:    qdrantURL,
		qdrantAPIKey: qdrantAPIKey,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		threshold:    0.95,
		ttl:          ttlSeconds,
		storeSem:     make(chan struct{}, storeSemCap),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.ensureCollection(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Lookup embeds key, searches Qdrant for a hit above the similarity threshold,
// and returns the cached response plus the embedding vector.
// The vector is returned even on a miss so callers can pass it to Store.
func (s *SemanticCache) Lookup(ctx context.Context, key, apiKeyHash string) (*models.ChatResponse, []float64, error) {
	vector, err := s.embed(ctx, key)
	if err != nil {
		log.Printf("cache: embed failed during lookup: %v", err)
		return nil, nil, nil
	}

	body := map[string]any{
		"vector":           vector,
		"limit":            1,
		"score_threshold":  s.threshold,
		"with_payload":     true,
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key":   "expires_at",
					"range": map[string]any{"gte": time.Now().Unix()},
				},
				{
					"key":   "api_key_hash",
					"match": map[string]any{"value": apiKeyHash},
				},
			},
		},
	}

	var result qdrantSearchResponse
	if err := s.qdrantDo(ctx, http.MethodPost,
		fmt.Sprintf("/collections/%s/points/search", collectionName),
		body, &result,
	); err != nil {
		log.Printf("cache: qdrant search failed: %v", err)
		return nil, vector, nil
	}

	if len(result.Result) == 0 {
		return nil, vector, nil
	}

	resp := result.Result[0].Payload.Response
	return &resp, vector, nil
}

// Store upserts resp into Qdrant using the pre-computed vector.
// It is always called asynchronously via AsyncStore; never returns an error.
func (s *SemanticCache) Store(ctx context.Context, key, apiKeyHash string, vector []float64, resp *models.ChatResponse) error {
	// Store a copy without the cache_hit flag so retrieved entries are clean.
	stored := *resp
	stored.CacheHit = false

	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	body := map[string]any{
		"points": []map[string]any{
			{
				"id":     id,
				"vector": vector,
				"payload": map[string]any{
					"expires_at":   time.Now().Unix() + s.ttl,
					"api_key_hash": apiKeyHash,
					"response":     stored,
				},
			},
		},
	}

	if err := s.qdrantDo(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s/points", collectionName),
		body, nil,
	); err != nil {
		log.Printf("cache: qdrant upsert failed: %v", err)
	}
	return nil
}

// AsyncStore launches a bounded goroutine to call Store. If the semaphore is
// full the store is silently skipped to prevent goroutine accumulation under load.
func (s *SemanticCache) AsyncStore(key, apiKeyHash string, vector []float64, resp *models.ChatResponse) {
	select {
	case s.storeSem <- struct{}{}:
		go func() {
			defer func() { <-s.storeSem }()
			_ = s.Store(context.Background(), key, apiKeyHash, vector, resp)
		}()
	default:
		log.Printf("cache: store semaphore full, skipping")
	}
}

// embed calls the OpenAI Embeddings API and returns the 1536-dim vector.
func (s *SemanticCache) embed(ctx context.Context, text string) ([]float64, error) {
	resp, err := s.openaiClient.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.F[openai.EmbeddingNewParamsInputUnion](shared.UnionString(text)),
		Model: openai.F(openai.EmbeddingModelTextEmbedding3Small),
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return resp.Data[0].Embedding, nil
}

// ensureCollection creates the Qdrant collection and payload indexes if missing.
func (s *SemanticCache) ensureCollection(ctx context.Context) error {
	// Check if the collection already exists.
	err := s.qdrantDo(ctx, http.MethodGet,
		fmt.Sprintf("/collections/%s", collectionName),
		nil, nil,
	)
	if err == nil {
		return nil // already exists
	}

	// Any error other than 404 is unexpected — surface it.
	qe, ok := err.(*qdrantError)
	if !ok || qe.status != http.StatusNotFound {
		return fmt.Errorf("cache: check collection: %w", err)
	}

	// Create the collection.
	if err := s.qdrantDo(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s", collectionName),
		map[string]any{
			"vectors": map[string]any{
				"size":     vectorDims,
				"distance": "Cosine",
			},
		}, nil,
	); err != nil {
		return fmt.Errorf("cache: create collection: %w", err)
	}

	// Create payload index on expires_at for range filter performance.
	if err := s.qdrantDo(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s/index", collectionName),
		map[string]any{"field_name": "expires_at", "field_schema": "integer"},
		nil,
	); err != nil {
		return fmt.Errorf("cache: create expires_at index: %w", err)
	}

	// Create payload index on api_key_hash for per-tenant filter performance.
	if err := s.qdrantDo(ctx, http.MethodPut,
		fmt.Sprintf("/collections/%s/index", collectionName),
		map[string]any{"field_name": "api_key_hash", "field_schema": "keyword"},
		nil,
	); err != nil {
		return fmt.Errorf("cache: create api_key_hash index: %w", err)
	}

	return nil
}

// qdrantDo executes a Qdrant REST request. Pass nil body for GET/HEAD.
// Pass nil result to ignore the response body (fire-and-forget upserts).
// Returns *qdrantError with the HTTP status on non-2xx responses.
func (s *SemanticCache) qdrantDo(ctx context.Context, method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.qdrantURL+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.qdrantAPIKey != "" {
		req.Header.Set("api-key", s.qdrantAPIKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &qdrantError{status: resp.StatusCode}
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}
	return nil
}

// qdrantError wraps a non-2xx HTTP status from Qdrant.
type qdrantError struct{ status int }

func (e *qdrantError) Error() string { return fmt.Sprintf("qdrant: HTTP %d", e.status) }

// Qdrant search response types.
type qdrantSearchResponse struct {
	Result []qdrantScoredPoint `json:"result"`
}

type qdrantScoredPoint struct {
	Score   float64        `json:"score"`
	Payload qdrantPayload  `json:"payload"`
}

type qdrantPayload struct {
	ExpiresAt  int64               `json:"expires_at"`
	APIKeyHash string              `json:"api_key_hash"`
	Response   models.ChatResponse `json:"response"`
}
