package reqlog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
)

// useTestLogger points the package-level slog default at buf and restores
// the previous default when the test finishes.
func useTestLogger(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func newTestRouter(setAPIKey bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/ping", func(c *gin.Context) {
		if setAPIKey {
			c.Set(ratelimit.APIKeyContextKey, "secret-key")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestMiddleware_SetsRequestIDHeaderAndLogsRequest(t *testing.T) {
	var buf bytes.Buffer
	useTestLogger(t, &buf)
	r := newTestRouter(false)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	id := w.Header().Get(RequestIDHeader)
	if id == "" {
		t.Fatal("expected X-Request-ID header to be set")
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("expected a single JSON log line, got %q: %v", buf.String(), err)
	}
	if entry["request_id"] != id {
		t.Errorf("log request_id = %v, want %v", entry["request_id"], id)
	}
	if entry["path"] != "/ping" {
		t.Errorf("log path = %v, want /ping", entry["path"])
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("log status = %v, want 200", entry["status"])
	}
	if entry["api_key_hash"] != "" {
		t.Errorf("expected empty api_key_hash when no API key set, got %v", entry["api_key_hash"])
	}
}

func TestMiddleware_HashesAPIKeyWhenPresent(t *testing.T) {
	var buf bytes.Buffer
	useTestLogger(t, &buf)
	r := newTestRouter(true)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("expected a single JSON log line: %v", err)
	}
	hash, _ := entry["api_key_hash"].(string)
	if hash == "" || hash == "secret-key" {
		t.Errorf("expected a non-empty hashed api_key_hash, got %q", hash)
	}
}

func TestMiddleware_UniqueRequestIDsAcrossRequests(t *testing.T) {
	var buf bytes.Buffer
	useTestLogger(t, &buf)
	r := newTestRouter(false)

	ids := make(map[string]bool)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		id := w.Header().Get(RequestIDHeader)
		if ids[id] {
			t.Fatalf("duplicate request ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestNewRequestID_NotEmpty(t *testing.T) {
	id := newRequestID()
	if strings.TrimSpace(id) == "" {
		t.Fatal("expected non-empty request ID")
	}
}
