package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// fakeVector returns a slice of n floats all set to v.
func fakeVector(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// embeddingServer starts a test HTTP server that returns a fake OpenAI embedding
// response containing vec and registers its cleanup on t.
func embeddingServer(t *testing.T, vec []float64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  "text-embedding-3-small",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": vec},
			},
			"usage": map[string]any{"prompt_tokens": 5, "total_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failingEmbeddingServer starts a test server that always returns 500.
func failingEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestCache creates a SemanticCache wired against the given test server URLs
// without calling ensureCollection.
func newTestCache(t *testing.T, qdrantURL, openaiURL string) *SemanticCache {
	t.Helper()
	return &SemanticCache{
		openaiClient: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(openaiURL),
		),
		qdrantURL:  qdrantURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		threshold:  0.95,
		ttl:        3600,
		storeSem:   make(chan struct{}, storeSemCap),
	}
}

// --- Lookup tests ---

func TestLookup_CacheHit(t *testing.T) {
	cachedResp := models.ChatResponse{
		ID:    "cached-1",
		Model: "gpt-4o-mini",
		Choices: []models.Choice{
			{Message: models.Message{Role: "assistant", Content: "4"}},
		},
		Usage: models.Usage{TotalTokens: 10},
	}

	var searchBody map[string]any
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&searchBody)
		payload, _ := json.Marshal(cachedResp)
		fmt.Fprintf(w, `{"result":[{"score":0.97,"payload":{"expires_at":%d,"api_key_hash":"abc","response":%s}}]}`,
			time.Now().Unix()+3600, payload)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.1))
	s := newTestCache(t, qdrant.URL, embed.URL)

	resp, vec, err := s.Lookup(context.Background(), "what is 2+2?", "abc")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected cache hit, got nil response")
	}
	if resp.ID != "cached-1" {
		t.Errorf("got ID %q, want %q", resp.ID, "cached-1")
	}
	if len(vec) != vectorDims {
		t.Errorf("got vector len %d, want %d", len(vec), vectorDims)
	}

	// Verify both filter conditions were sent.
	filter, _ := searchBody["filter"].(map[string]any)
	must, _ := filter["must"].([]any)
	if len(must) != 2 {
		t.Errorf("expected 2 filter conditions, got %d", len(must))
	}
}

func TestLookup_CacheMiss_ReturnsVector(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":[]}`)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.2))
	s := newTestCache(t, qdrant.URL, embed.URL)

	resp, vec, err := s.Lookup(context.Background(), "anything", "abc")

	if err != nil || resp != nil {
		t.Errorf("expected (nil,vec,nil) on miss, got resp=%v err=%v", resp, err)
	}
	// Vector must be returned even on miss so the caller can reuse it for Store.
	if len(vec) != vectorDims {
		t.Errorf("expected vector on miss, got len %d", len(vec))
	}
}

func TestLookup_EmbedFailure(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":[]}`)
	}))
	t.Cleanup(qdrant.Close)

	embed := failingEmbeddingServer(t)
	s := newTestCache(t, qdrant.URL, embed.URL)

	resp, vec, err := s.Lookup(context.Background(), "hello", "abc")

	if err != nil || resp != nil || vec != nil {
		t.Errorf("expected (nil,nil,nil) on embed failure, got resp=%v vec=%v err=%v", resp, vec, err)
	}
}

func TestLookup_QdrantFailure(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.3))
	s := newTestCache(t, qdrant.URL, embed.URL)

	resp, vec, err := s.Lookup(context.Background(), "hello", "abc")

	if err != nil || resp != nil {
		t.Errorf("expected (nil,vec,nil) on qdrant failure, got resp=%v err=%v", resp, err)
	}
	// Vector is still returned even when Qdrant fails.
	if len(vec) != vectorDims {
		t.Errorf("expected vector on qdrant failure, got len %d", len(vec))
	}
}

func TestLookup_TenantIsolation(t *testing.T) {
	var capturedBody map[string]any
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		fmt.Fprint(w, `{"result":[]}`)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.1))
	s := newTestCache(t, qdrant.URL, embed.URL)
	_, _, _ = s.Lookup(context.Background(), "prompt", "tenanthash123")

	filter, _ := capturedBody["filter"].(map[string]any)
	must, _ := filter["must"].([]any)
	found := false
	for _, c := range must {
		cond, _ := c.(map[string]any)
		if cond["key"] == "api_key_hash" {
			if match, ok := cond["match"].(map[string]any); ok {
				if match["value"] == "tenanthash123" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("api_key_hash filter not present in Qdrant search request")
	}
}

// --- Store tests ---

func TestStore_SkipsReEmbed(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":{"operation_id":0,"status":"completed"}}`)
	}))
	t.Cleanup(qdrant.Close)

	// OpenAI should NOT be called — Store uses the pre-computed vector.
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Store called OpenAI embeddings — should use pre-computed vector")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(embed.Close)

	s := newTestCache(t, qdrant.URL, embed.URL)
	err := s.Store(context.Background(), "key1", "hash1", fakeVector(vectorDims, 0.5), &models.ChatResponse{})
	if err != nil {
		t.Errorf("Store returned unexpected error: %v", err)
	}
}

func TestStore_Success_PayloadShape(t *testing.T) {
	var capturedUpsert map[string]any
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedUpsert)
		fmt.Fprint(w, `{"result":{"operation_id":0,"status":"completed"}}`)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.1)) // should not be called
	s := newTestCache(t, qdrant.URL, embed.URL)

	resp := &models.ChatResponse{
		ID:       "r1",
		Model:    "gpt-4o",
		Usage:    models.Usage{TotalTokens: 20},
		CacheHit: true, // must be stripped before storing
	}
	_ = s.Store(context.Background(), "k1", "h1", fakeVector(vectorDims, 0.5), resp)

	points, _ := capturedUpsert["points"].([]any)
	if len(points) != 1 {
		t.Fatalf("expected 1 upserted point, got %d", len(points))
	}
	point, _ := points[0].(map[string]any)
	payload, _ := point["payload"].(map[string]any)

	if expiresAt, _ := payload["expires_at"].(float64); int64(expiresAt) <= time.Now().Unix() {
		t.Error("expires_at should be in the future")
	}
	if payload["api_key_hash"] != "h1" {
		t.Errorf("unexpected api_key_hash: %v", payload["api_key_hash"])
	}

	// CacheHit must be stripped.
	storedResp, _ := payload["response"].(map[string]any)
	if ch, ok := storedResp["cache_hit"].(bool); ok && ch {
		t.Error("stored response should not have cache_hit=true")
	}
}

func TestStore_QdrantFailure_ReturnsNil(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(qdrant.Close)

	embed := embeddingServer(t, fakeVector(vectorDims, 0.1))
	s := newTestCache(t, qdrant.URL, embed.URL)

	err := s.Store(context.Background(), "k", "h", fakeVector(vectorDims, 0.1), &models.ChatResponse{})
	if err != nil {
		t.Errorf("Store should always return nil, got: %v", err)
	}
}

// --- ensureCollection / New tests ---

func TestNew_EnsureCollection_AlreadyExists(t *testing.T) {
	var putCalled bool
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"result":{"name":"semantic_cache"}}`)
			return
		}
		putCalled = true
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(qdrant.Close)

	_, err := New("fake-key", qdrant.URL, "", 3600)
	if err != nil {
		t.Fatalf("New should succeed when collection exists: %v", err)
	}
	if putCalled {
		t.Error("should not PUT collection when it already exists")
	}
}

func TestNew_EnsureCollection_Creates(t *testing.T) {
	var paths []string
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+":"+r.URL.Path)
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"result":{"operation_id":0,"status":"completed"}}`)
	}))
	t.Cleanup(qdrant.Close)

	_, err := New("fake-key", qdrant.URL, "", 3600)
	if err != nil {
		t.Fatalf("New should succeed after creating collection: %v", err)
	}
	// GET collection + PUT collection + PUT index×2 = 4 calls minimum.
	if len(paths) < 4 {
		t.Errorf("expected ≥4 Qdrant calls, got %d: %v", len(paths), paths)
	}
}

func TestNew_EnsureCollection_Failure(t *testing.T) {
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	t.Cleanup(qdrant.Close)

	_, err := New("fake-key", qdrant.URL, "", 3600)
	if err == nil {
		t.Error("New should return error when Qdrant is unavailable")
	}
}

func TestNew_CreatesPayloadIndexes(t *testing.T) {
	var indexBodies []map[string]any
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(r.URL.Path, "index") {
			indexBodies = append(indexBodies, body)
		}
		fmt.Fprint(w, `{"result":{"operation_id":0,"status":"completed"}}`)
	}))
	t.Cleanup(qdrant.Close)

	_, err := New("fake-key", qdrant.URL, "", 3600)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	fields := make(map[string]bool)
	for _, body := range indexBodies {
		if name, ok := body["field_name"].(string); ok {
			fields[name] = true
		}
	}
	if !fields["expires_at"] {
		t.Error("expires_at payload index not created")
	}
	if !fields["api_key_hash"] {
		t.Error("api_key_hash payload index not created")
	}
}
