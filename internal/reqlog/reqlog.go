// Package reqlog provides per-request structured logging: a short request ID
// correlated across the access log line and the gateway's fallback-loop
// warnings, and an X-Request-ID response header for client-side correlation.
package reqlog

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
)

const (
	RequestIDContextKey = "requestID"
	RequestIDHeader     = "X-Request-ID"
)

// Middleware assigns a request ID to every request, exposes it via the
// X-Request-ID response header and the Gin context, and emits one
// structured "request" log line after the request completes. The API key
// (if present) is logged as a SHA-256 hash, never in the clear.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := newRequestID()
		c.Set(RequestIDContextKey, id)
		c.Header(RequestIDHeader, id)

		start := time.Now()
		c.Next()

		apiKeyHash := ""
		if key := c.GetString(ratelimit.APIKeyContextKey); key != "" {
			apiKeyHash = hashAPIKey(key)
		}

		slog.Info("request",
			"request_id", id,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Seconds()*1000,
			"client_ip", c.ClientIP(),
			"api_key_hash", apiKeyHash,
		)
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
