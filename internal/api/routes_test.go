package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/pavanv25/ai-gateway/internal/alias"
	"github.com/pavanv25/ai-gateway/internal/metrics"
	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- MockCache ---

// MockCache implements cache.Cache for testing.
type MockCache struct {
	mu         sync.Mutex
	lookupResp *models.ChatResponse
	lookupVec  []float64
	stored     []storeCall
	storeDone  chan struct{} // closed when AsyncStore is called once
}

type storeCall struct {
	key        string
	apiKeyHash string
	vector     []float64
	resp       *models.ChatResponse
}

func newMockCache(hit *models.ChatResponse, vec []float64) *MockCache {
	return &MockCache{
		lookupResp: hit,
		lookupVec:  vec,
		storeDone:  make(chan struct{}),
	}
}

func (m *MockCache) Lookup(_ context.Context, key, apiKeyHash string) (*models.ChatResponse, []float64, error) {
	return m.lookupResp, m.lookupVec, nil
}

func (m *MockCache) Store(_ context.Context, key, apiKeyHash string, vector []float64, resp *models.ChatResponse) error {
	m.mu.Lock()
	m.stored = append(m.stored, storeCall{key, apiKeyHash, vector, resp})
	m.mu.Unlock()
	return nil
}

func (m *MockCache) AsyncStore(key, apiKeyHash string, vector []float64, resp *models.ChatResponse) {
	_ = m.Store(context.Background(), key, apiKeyHash, vector, resp)
	select {
	case <-m.storeDone:
	default:
		close(m.storeDone)
	}
}

func (m *MockCache) getStored() []storeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]storeCall(nil), m.stored...)
}

// --- Test helpers ---

func newTestLimiter(t *testing.T, tpmLimit int) *ratelimit.Limiter {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return ratelimit.New(rdb, ratelimit.Config{TPMLimit: tpmLimit})
}

func newTestRouter(limiter *ratelimit.Limiter, providers map[string]provider.Provider, c *MockCache) *gin.Engine {
	return newTestRouterWithStore(limiter, providers, c, nil)
}

func newTestRouterWithStore(limiter *ratelimit.Limiter, providers map[string]provider.Provider, c *MockCache, store *metrics.Store) *gin.Engine {
	r := gin.New()
	var cache interface {
		Lookup(context.Context, string, string) (*models.ChatResponse, []float64, error)
		Store(context.Context, string, string, []float64, *models.ChatResponse) error
		AsyncStore(string, string, []float64, *models.ChatResponse)
	}
	if c != nil {
		cache = c
	}
	RegisterRoutes(r, limiter, providers, nil, cache, store)
	return r
}

func chatBody(provider, model, userMsg string) string {
	return `{"provider":"` + provider + `","model":"` + model + `","messages":[{"role":"user","content":"` + userMsg + `"}]}`
}

func doRequest(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- Tests ---

func TestBuildCacheKey_IncludesModelAndSystemPrompt(t *testing.T) {
	req1 := &models.ChatRequest{
		Model:    "gpt-4o",
		Messages: []models.Message{{Role: "user", Content: "Hello"}},
	}
	req2 := &models.ChatRequest{
		Model:    "gpt-4o-mini", // different model
		Messages: []models.Message{{Role: "user", Content: "Hello"}},
	}
	req3 := &models.ChatRequest{
		Model: "gpt-4o",
		Messages: []models.Message{
			{Role: "system", Content: "You are a poet"}, // different system prompt
			{Role: "user", Content: "Hello"},
		},
	}

	k1, _ := buildCacheKey(req1)
	k2, _ := buildCacheKey(req2)
	k3, _ := buildCacheKey(req3)

	if k1 == "" {
		t.Error("expected non-empty key")
	}
	if k1 == k2 {
		t.Error("different models should produce different cache keys")
	}
	if k1 == k3 {
		t.Error("different system prompts should produce different cache keys")
	}
}

func TestBuildCacheKey_NoUserMessage(t *testing.T) {
	req := &models.ChatRequest{
		Model:    "gpt-4o",
		Messages: []models.Message{{Role: "system", Content: "only system"}},
	}
	key, msg := buildCacheKey(req)
	if key != "" || msg != "" {
		t.Errorf("expected empty key and msg, got key=%q msg=%q", key, msg)
	}
}

func TestBuildCacheKey_LastUserMessageUsed(t *testing.T) {
	req := &models.ChatRequest{
		Model: "gpt-4o",
		Messages: []models.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "response"},
			{Role: "user", Content: "second"},
		},
	}
	_, lastUser := buildCacheKey(req)
	if lastUser != "second" {
		t.Errorf("expected last user message %q, got %q", "second", lastUser)
	}
}

func TestChatHandler_CacheHit(t *testing.T) {
	cachedResp := &models.ChatResponse{
		ID:    "cached-id",
		Model: "gpt-4o",
		Choices: []models.Choice{
			{Message: models.Message{Role: "assistant", Content: "cached answer"}},
		},
		Usage: models.Usage{TotalTokens: 5},
	}
	mc := newMockCache(cachedResp, []float64{0.1, 0.2})

	limiter := newTestLimiter(t, 10000)
	// MockProvider should NOT be called on a cache hit.
	mp := provider.NewMockProvider("should not appear")
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": mp}, mc)

	w := doRequest(r, chatBody("mock", "mock", "what is 2+2?"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ChatResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if !resp.CacheHit {
		t.Error("expected cache_hit=true")
	}
	if resp.ResolvedProvider != "cache" {
		t.Errorf("expected resolved_provider=cache, got %q", resp.ResolvedProvider)
	}
	if resp.ID != "cached-id" {
		t.Errorf("expected cached response ID, got %q", resp.ID)
	}
}

func TestChatHandler_CacheHit_RateLimitExceeded(t *testing.T) {
	cachedResp := &models.ChatResponse{
		Usage: models.Usage{TotalTokens: 99999},
	}
	mc := newMockCache(cachedResp, []float64{0.1})

	limiter := newTestLimiter(t, 100) // tiny limit — cached response exceeds it
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": provider.NewMockProvider("x")}, mc)

	w := doRequest(r, chatBody("mock", "mock", "hello"))

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestChatHandler_CacheMiss_StoresOnSuccess(t *testing.T) {
	// No cache hit (lookupResp=nil), but provide a vector so Store can be called.
	vec := []float64{0.1, 0.2, 0.3}
	mc := newMockCache(nil, vec)

	limiter := newTestLimiter(t, 10000)
	mp := provider.NewMockProvider("the answer")
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": mp}, mc)

	w := doRequest(r, chatBody("mock", "mock", "what is 2+2?"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ChatResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.CacheHit {
		t.Error("cache_hit should be false on a miss")
	}

	// Wait for AsyncStore goroutine.
	<-mc.storeDone

	stored := mc.getStored()
	if len(stored) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(stored))
	}
	if len(stored[0].vector) != len(vec) {
		t.Error("stored vector should match the pre-computed lookup vector")
	}
}

func TestChatHandler_NilCache_NoPanic(t *testing.T) {
	limiter := newTestLimiter(t, 10000)
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": provider.NewMockProvider("ok")}, nil)

	w := doRequest(r, chatBody("mock", "mock", "hello"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestChatHandler_NoUserMessage_SkipsCache(t *testing.T) {
	// A request with only a system message — cache should be bypassed entirely.
	mc := newMockCache(&models.ChatResponse{ID: "should-not-appear"}, []float64{0.1})

	limiter := newTestLimiter(t, 10000)
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": provider.NewMockProvider("ok")}, mc)

	body := `{"provider":"mock","model":"mock","messages":[{"role":"system","content":"be helpful"}]}`
	w := doRequest(r, body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ChatResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.CacheHit {
		t.Error("cache should be bypassed when there is no user message")
	}
}

func TestStreamHandler_CacheHit_ReturnsJSON(t *testing.T) {
	cachedResp := &models.ChatResponse{
		ID:    "stream-cached",
		Model: "gpt-4o",
		Choices: []models.Choice{
			{Message: models.Message{Role: "assistant", Content: "stream answer"}},
		},
		Usage: models.Usage{TotalTokens: 8},
	}
	mc := newMockCache(cachedResp, []float64{0.5})

	limiter := newTestLimiter(t, 10000)
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": provider.NewMockProvider("x")}, mc)

	body := `{"provider":"mock","model":"mock","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("cache hit with stream=true should return application/json, got %q", ct)
	}
	var resp models.ChatResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.CacheHit {
		t.Error("expected cache_hit=true on streaming cache hit")
	}
}

func TestStreamHandler_CacheMiss_StoresAccumulatedText(t *testing.T) {
	vec := []float64{0.7, 0.8}
	mc := newMockCache(nil, vec)

	limiter := newTestLimiter(t, 10000)
	mp := provider.NewMockProvider("hello world")
	r := newTestRouter(limiter, map[string]provider.Provider{"mock": mp}, mc)

	body := `{"provider":"mock","model":"mock","stream":true,"messages":[{"role":"user","content":"say hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Wait for the async Store goroutine.
	<-mc.storeDone

	stored := mc.getStored()
	if len(stored) != 1 {
		t.Fatalf("expected 1 store call after stream, got %d", len(stored))
	}
	content := stored[0].resp.Choices[0].Message.Content
	if content == "" {
		t.Error("stored streaming response should have non-empty content")
	}
	if !strings.Contains(content, "hello") {
		t.Errorf("stored content should contain stream text, got %q", content)
	}
}

// --- GET /v1/metrics tests ---

func doMetricsRequest(r *gin.Engine, query string) *httptest.ResponseRecorder {
	path := "/v1/metrics"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMetricsHandler_ReturnsSnapshotJSON(t *testing.T) {
	store := metrics.NewStore()
	store.Record(metrics.MetricEvent{
		Timestamp:    time.Now(),
		Provider:     "openai",
		Model:        "gpt-4o",
		TotalTokens:  100,
		CostUSD:      0.001,
		CacheHit:     false,
	})

	limiter := newTestLimiter(t, 10000)
	r := newTestRouterWithStore(limiter, map[string]provider.Provider{}, nil, store)

	w := doMetricsRequest(r, "window=1h")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var snap metrics.Snapshot
	if err := json.NewDecoder(w.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode snapshot: %v", err)
	}
	if snap.Totals.RequestCount != 1 {
		t.Errorf("RequestCount: want 1, got %d", snap.Totals.RequestCount)
	}
	if snap.Totals.TotalTokens != 100 {
		t.Errorf("TotalTokens: want 100, got %d", snap.Totals.TotalTokens)
	}
	if len(snap.Breakdowns) != 1 {
		t.Errorf("expected 1 breakdown, got %d", len(snap.Breakdowns))
	}
}

func TestMetricsHandler_DefaultWindowIs5m(t *testing.T) {
	store := metrics.NewStore()
	limiter := newTestLimiter(t, 10000)
	r := newTestRouterWithStore(limiter, map[string]provider.Provider{}, nil, store)

	w := doMetricsRequest(r, "") // no window param
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var snap metrics.Snapshot
	_ = json.NewDecoder(w.Body).Decode(&snap)
	if snap.Window != 5*time.Minute {
		t.Errorf("default window: want 5m, got %v", snap.Window)
	}
}

func TestMetricsHandler_InvalidWindowReturns400(t *testing.T) {
	store := metrics.NewStore()
	limiter := newTestLimiter(t, 10000)
	r := newTestRouterWithStore(limiter, map[string]provider.Provider{}, nil, store)

	w := doMetricsRequest(r, "window=notaduration")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestMetricsHandler_WindowCappedAt1h(t *testing.T) {
	store := metrics.NewStore()
	limiter := newTestLimiter(t, 10000)
	r := newTestRouterWithStore(limiter, map[string]provider.Provider{}, nil, store)

	w := doMetricsRequest(r, "window=24h")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var snap metrics.Snapshot
	_ = json.NewDecoder(w.Body).Decode(&snap)
	if snap.Window != time.Hour {
		t.Errorf("window should be capped at 1h, got %v", snap.Window)
	}
}

func TestMetricsHandler_NoStoreReturns404(t *testing.T) {
	// When store is nil, /v1/metrics route is not registered.
	limiter := newTestLimiter(t, 10000)
	r := newTestRouter(limiter, map[string]provider.Provider{}, nil)

	w := doMetricsRequest(r, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when store is nil, got %d", w.Code)
	}
}

func TestMetricsHandler_RequiresAuth(t *testing.T) {
	store := metrics.NewStore()
	limiter := newTestLimiter(t, 10000)
	r := newTestRouterWithStore(limiter, map[string]provider.Provider{}, nil, store)

	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil) // no X-API-Key
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}
}

// circuitOpenProvider simulates a provider whose circuit is open.
type circuitOpenProvider struct{}

func (p *circuitOpenProvider) Name() string { return "circuit-open" }
func (p *circuitOpenProvider) Chat(_ context.Context, _ *models.ChatRequest) (*models.ChatResponse, error) {
	return nil, provider.ErrCircuitOpen
}
func (p *circuitOpenProvider) ChatStream(_ context.Context, _ *models.ChatRequest) (<-chan models.StreamEvent, error) {
	return nil, provider.ErrCircuitOpen
}

func TestChatHandler_CircuitOpenSkippedFallbackContinues(t *testing.T) {
	cfgYAML := `tasks:
  test-task:
    - provider: circuit-open
      model: any
    - provider: mock
      model: mock
`
	cfgFile := filepath.Join(t.TempDir(), "aliases.yaml")
	if err := os.WriteFile(cfgFile, []byte(cfgYAML), 0600); err != nil {
		t.Fatal(err)
	}

	resolver, err := alias.Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	limiter := newTestLimiter(t, 10000)
	providers := map[string]provider.Provider{
		"circuit-open": &circuitOpenProvider{},
		"mock":         provider.NewMockProvider("fallback response"),
	}

	r := gin.New()
	RegisterRoutes(r, limiter, providers, resolver, nil, nil /*store*/)

	body := `{"task":"test-task","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp models.ChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ResolvedProvider != "mock" {
		t.Errorf("expected resolved_provider=mock, got %q", resp.ResolvedProvider)
	}
}
