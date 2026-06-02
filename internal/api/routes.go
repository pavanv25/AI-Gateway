package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pavanv25/ai-gateway/internal/alias"
	"github.com/pavanv25/ai-gateway/internal/cache"
	"github.com/pavanv25/ai-gateway/internal/metrics"
	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// RegisterRoutes wires all application routes onto r.
// limiter, providers, resolver, and c are injected — no global state.
// resolver may be nil when the alias feature is disabled.
// c may be nil when the semantic cache is disabled.
// collector may be nil, in which case a NoopCollector is used.
func RegisterRoutes(r *gin.Engine, limiter *ratelimit.Limiter, providers map[string]provider.Provider, resolver *alias.Resolver, c cache.Cache, collector metrics.Collector) {
	if collector == nil {
		collector = metrics.NoopCollector{}
	}
	v1 := r.Group("/v1")
	v1.Use(ratelimit.AuthMiddleware())
	{
		v1.POST("/chat", chatHandler(limiter, providers, resolver, c, collector))
	}
}

func chatHandler(limiter *ratelimit.Limiter, providers map[string]provider.Provider, resolver *alias.Resolver, c cache.Cache, collector metrics.Collector) gin.HandlerFunc {
	return func(gc *gin.Context) {
		requestStart := time.Now()

		var req models.ChatRequest
		if err := gc.ShouldBindJSON(&req); err != nil {
			gc.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		apiKey := gc.GetString(ratelimit.APIKeyContextKey)
		ctx := gc.Request.Context()

		entries, status, errMsg := resolveEntries(&req, resolver)
		if errMsg != "" {
			gc.JSON(status, gin.H{"error": errMsg})
			return
		}

		// Semantic cache lookup — before rate limiting or provider calls.
		cacheKey, _ := buildCacheKey(&req)
		apiKeyHash := sha256hex(apiKey)
		var cacheVector []float64
		var cacheLatencyMs float64

		if c != nil && cacheKey != "" {
			cacheLookupStart := time.Now()
			cached, vec, _ := c.Lookup(ctx, cacheKey, apiKeyHash)
			cacheLatencyMs = time.Since(cacheLookupStart).Seconds() * 1000
			cacheVector = vec
			if cached != nil {
				// Cache hit: apply token budget (bypasses provider but not rate limit).
				token, err := limiter.Reserve(ctx, apiKey, cached.Usage.TotalTokens)
				if err != nil {
					if errors.Is(err, ratelimit.ErrLimitExceeded) {
						gc.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
						return
					}
					log.Printf("reserve error: %v", err)
					gc.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
					return
				}
				_ = limiter.Commit(ctx, apiKey, token, cached.Usage.TotalTokens)
				cached.CacheHit = true
				cached.ResolvedProvider = "cache"
				collector.Record(metrics.MetricEvent{
					Timestamp:        time.Now(),
					Provider:         "cache",
					Model:            req.Model,
					APIKeyHash:       apiKeyHash,
					PromptTokens:     cached.Usage.PromptTokens,
					CompletionTokens: cached.Usage.CompletionTokens,
					TotalTokens:      cached.Usage.TotalTokens,
					CacheHit:         true,
					Stream:           req.Stream,
					RequestLatencyMs: time.Since(requestStart).Seconds() * 1000,
					CacheLatencyMs:   cacheLatencyMs,
				})
				gc.Header("Content-Type", "application/json")
				gc.JSON(http.StatusOK, cached)
				return
			}
		}

		if req.Stream {
			handleStreamWithFallback(gc, entries, providers, &req, limiter, apiKey, c, cacheKey, apiKeyHash, cacheVector, requestStart, cacheLatencyMs, collector)
		} else {
			handleChatWithFallback(gc, entries, providers, &req, limiter, apiKey, c, cacheKey, apiKeyHash, cacheVector, requestStart, cacheLatencyMs, collector)
		}
	}
}

// buildCacheKey returns a SHA-256 hex key over {model|system_message|last_user_message}
// and the raw last user message. Returns ("", "") when there is no user message
// (cache should be bypassed).
func buildCacheKey(req *models.ChatRequest) (key, lastUserMsg string) {
	var systemMsg, lastUser string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemMsg = m.Content
		case "user":
			lastUser = m.Content
		}
	}
	if lastUser == "" {
		return "", ""
	}
	raw := req.Model + "|" + systemMsg + "|" + lastUser
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:]), lastUser
}

// sha256hex returns the hex-encoded SHA-256 digest of s.
func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// resolveEntries returns the ordered provider+model list for this request.
// On error it returns a non-empty errMsg and the appropriate HTTP status code.
func resolveEntries(req *models.ChatRequest, resolver *alias.Resolver) ([]alias.Entry, int, string) {
	if req.Task != "" {
		if !resolver.Enabled() {
			return nil, http.StatusBadRequest, "task field requires alias config (ALIAS_CONFIG not set)"
		}
		if req.Provider != "" || req.Model != "" {
			log.Printf("warn: both task and provider/model set — task %q takes precedence", req.Task)
		}
		entries, err := resolver.Resolve(req.Task)
		if err != nil {
			return nil, http.StatusBadRequest, err.Error()
		}
		return entries, 0, ""
	}

	if req.Provider == "" {
		return nil, http.StatusBadRequest, "provider or task is required"
	}
	return []alias.Entry{{Provider: req.Provider, Model: req.Model}}, 0, ""
}

func handleChatWithFallback(
	gc *gin.Context,
	entries []alias.Entry,
	providers map[string]provider.Provider,
	req *models.ChatRequest,
	limiter *ratelimit.Limiter,
	apiKey string,
	c cache.Cache,
	cacheKey, apiKeyHash string,
	cacheVector []float64,
	requestStart time.Time,
	cacheLatencyMs float64,
	collector metrics.Collector,
) {
	ctx := gc.Request.Context()
	var lastErr error

	for i, entry := range entries {
		p, ok := providers[entry.Provider]
		if !ok {
			log.Printf("alias: provider %q not registered, skipping entry %d/%d", entry.Provider, i+1, len(entries))
			lastErr = errors.New("no available provider")
			continue
		}

		attempt := *req
		attempt.Provider = entry.Provider
		attempt.Model = entry.Model

		token, err := limiter.Reserve(ctx, apiKey, attempt.MaxTokens)
		if err != nil {
			if errors.Is(err, ratelimit.ErrLimitExceeded) {
				gc.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
				return
			}
			log.Printf("reserve error: %v", err)
			gc.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
			return
		}

		providerStart := time.Now()
		resp, err := p.Chat(ctx, &attempt)
		providerMs := time.Since(providerStart).Seconds() * 1000

		if err != nil {
			_ = limiter.Commit(ctx, apiKey, token, 0)
			lastErr = err
			if ctx.Err() != nil || !provider.IsRetriable(err) {
				break
			}
			if i < len(entries)-1 {
				if errors.Is(err, provider.ErrCircuitOpen) {
					log.Printf("alias: attempt %d/%d skipped — circuit open for %q, trying next entry", i+1, len(entries), entry.Provider)
				} else {
					log.Printf("alias: attempt %d/%d failed (%v), trying next entry", i+1, len(entries), err)
				}
			}
			continue
		}

		if err := limiter.Commit(ctx, apiKey, token, resp.Usage.TotalTokens); err != nil {
			log.Printf("commit error (non-fatal): %v", err)
		}
		resp.ResolvedProvider = entry.Provider

		// Store in cache — best-effort, reuses the vector from the Lookup call.
		if c != nil && cacheKey != "" && cacheVector != nil {
			c.AsyncStore(cacheKey, apiKeyHash, cacheVector, resp)
		}

		collector.Record(metrics.MetricEvent{
			Timestamp:         time.Now(),
			Provider:          entry.Provider,
			Model:             attempt.Model,
			APIKeyHash:        apiKeyHash,
			PromptTokens:      resp.Usage.PromptTokens,
			CompletionTokens:  resp.Usage.CompletionTokens,
			TotalTokens:       resp.Usage.TotalTokens,
			RequestLatencyMs:  time.Since(requestStart).Seconds() * 1000,
			ProviderLatencyMs: providerMs,
			CacheLatencyMs:    cacheLatencyMs,
			FallbackAttempts:  i,
		})

		gc.JSON(http.StatusOK, resp)
		return
	}

	if lastErr == nil {
		lastErr = errors.New("no available provider for this request")
	}
	collector.Record(metrics.MetricEvent{
		Timestamp:        time.Now(),
		Provider:         req.Provider,
		Model:            req.Model,
		APIKeyHash:       apiKeyHash,
		RequestLatencyMs: time.Since(requestStart).Seconds() * 1000,
		CacheLatencyMs:   cacheLatencyMs,
		FallbackAttempts: len(entries),
		ErrorType:        "no_provider",
	})
	gc.JSON(http.StatusBadGateway, gin.H{"error": lastErr.Error()})
}

func handleStreamWithFallback(
	gc *gin.Context,
	entries []alias.Entry,
	providers map[string]provider.Provider,
	req *models.ChatRequest,
	limiter *ratelimit.Limiter,
	apiKey string,
	c cache.Cache,
	cacheKey, apiKeyHash string,
	cacheVector []float64,
	requestStart time.Time,
	cacheLatencyMs float64,
	collector metrics.Collector,
) {
	ctx := gc.Request.Context()
	var lastErr error
	headersSent := false

	for i, entry := range entries {
		p, ok := providers[entry.Provider]
		if !ok {
			log.Printf("alias: provider %q not registered, skipping entry %d/%d", entry.Provider, i+1, len(entries))
			lastErr = errors.New("no available provider")
			continue
		}

		attempt := *req
		attempt.Provider = entry.Provider
		attempt.Model = entry.Model

		token, err := limiter.Reserve(ctx, apiKey, attempt.MaxTokens)
		if err != nil {
			if errors.Is(err, ratelimit.ErrLimitExceeded) {
				if !headersSent {
					gc.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
				}
				return
			}
			log.Printf("reserve error: %v", err)
			if !headersSent {
				gc.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
			}
			return
		}

		providerStart := time.Now()
		ch, err := p.ChatStream(ctx, &attempt)
		if err != nil {
			_ = limiter.Commit(ctx, apiKey, token, 0)
			lastErr = err
			if ctx.Err() != nil || !provider.IsRetriable(err) {
				break
			}
			if i < len(entries)-1 {
				if errors.Is(err, provider.ErrCircuitOpen) {
					log.Printf("alias: stream attempt %d/%d skipped — circuit open for %q, trying next", i+1, len(entries), entry.Provider)
				} else {
					log.Printf("alias: stream attempt %d/%d failed to start (%v), trying next", i+1, len(entries), err)
				}
			}
			continue
		}

		// Read events. SSE headers are sent lazily on the first content delta
		// so we can still fail over if the stream errors before sending anything.
		// Accumulate deltas for cache storage.
		var contentBuilder strings.Builder
		contentSent := false
		success := false
		retry := false
		var finalUsage *models.Usage

	eventLoop:
		for {
			select {
			case <-ctx.Done():
				_ = limiter.Commit(ctx, apiKey, token, 0)
				return

			case event, ok := <-ch:
				if !ok {
					_ = limiter.Commit(ctx, apiKey, token, 0)
					if !contentSent {
						lastErr = errors.New("stream closed unexpectedly")
						retry = provider.IsRetriable(lastErr)
					}
					break eventLoop
				}

				if event.Done {
					if event.Err != nil && !contentSent {
						_ = limiter.Commit(ctx, apiKey, token, 0)
						lastErr = event.Err
						retry = ctx.Err() == nil && provider.IsRetriable(event.Err)
						if retry && i < len(entries)-1 {
							if errors.Is(event.Err, provider.ErrCircuitOpen) {
								log.Printf("alias: stream attempt %d/%d skipped — circuit open for %q, trying next", i+1, len(entries), entry.Provider)
							} else {
								log.Printf("alias: stream attempt %d/%d failed before content (%v), trying next", i+1, len(entries), event.Err)
							}
						}
						break eventLoop
					}
					actual := 0
					if event.Usage != nil {
						actual = event.Usage.TotalTokens
						finalUsage = event.Usage
					}
					if err := limiter.Commit(ctx, apiKey, token, actual); err != nil {
						log.Printf("stream commit (non-fatal): %v", err)
					}
					success = true
					break eventLoop
				}

				// Normal content delta — commit to this stream.
				if !headersSent {
					gc.Writer.Header().Set("Content-Type", "text/event-stream")
					gc.Writer.Header().Set("Cache-Control", "no-cache")
					gc.Writer.Header().Set("Connection", "keep-alive")
					headersSent = true
				}
				contentSent = true
				contentBuilder.WriteString(event.Delta)
				gc.SSEvent("message", event)
				gc.Writer.Flush()
			}
		}

		if success {
			providerMs := time.Since(providerStart).Seconds() * 1000
			usage := models.Usage{}
			if finalUsage != nil {
				usage = *finalUsage
			}
			// Store the assembled response in cache — best-effort.
			if c != nil && cacheKey != "" && cacheVector != nil {
				synthesized := &models.ChatResponse{
					Model:            attempt.Model,
					ResolvedProvider: entry.Provider,
					Choices: []models.Choice{
						{Message: models.Message{Role: "assistant", Content: contentBuilder.String()}},
					},
					Usage: usage,
				}
				c.AsyncStore(cacheKey, apiKeyHash, cacheVector, synthesized)
			}
			collector.Record(metrics.MetricEvent{
				Timestamp:         time.Now(),
				Provider:          entry.Provider,
				Model:             attempt.Model,
				APIKeyHash:        apiKeyHash,
				PromptTokens:      usage.PromptTokens,
				CompletionTokens:  usage.CompletionTokens,
				TotalTokens:       usage.TotalTokens,
				Stream:            true,
				RequestLatencyMs:  time.Since(requestStart).Seconds() * 1000,
				ProviderLatencyMs: providerMs,
				CacheLatencyMs:    cacheLatencyMs,
				FallbackAttempts:  i,
			})
			return
		}
		if !retry {
			break
		}
	}

	if !headersSent {
		if lastErr == nil {
			lastErr = errors.New("no available provider for this request")
		}
		collector.Record(metrics.MetricEvent{
			Timestamp:        time.Now(),
			Provider:         req.Provider,
			Model:            req.Model,
			APIKeyHash:       apiKeyHash,
			Stream:           true,
			RequestLatencyMs: time.Since(requestStart).Seconds() * 1000,
			CacheLatencyMs:   cacheLatencyMs,
			FallbackAttempts: len(entries),
			ErrorType:        "no_provider",
		})
		gc.JSON(http.StatusBadGateway, gin.H{"error": lastErr.Error()})
	}
}
