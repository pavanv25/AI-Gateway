package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pavanv25/ai-gateway/internal/alias"
	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// RegisterRoutes wires all application routes onto r.
// limiter, providers, and resolver are injected — no global state.
// resolver may be nil when the alias feature is disabled.
func RegisterRoutes(r *gin.Engine, limiter *ratelimit.Limiter, providers map[string]provider.Provider, resolver *alias.Resolver) {
	v1 := r.Group("/v1")
	v1.Use(ratelimit.AuthMiddleware())
	{
		v1.POST("/chat", chatHandler(limiter, providers, resolver))
	}
}

func chatHandler(limiter *ratelimit.Limiter, providers map[string]provider.Provider, resolver *alias.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req models.ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		apiKey := c.GetString(ratelimit.APIKeyContextKey)

		entries, status, errMsg := resolveEntries(&req, resolver)
		if errMsg != "" {
			c.JSON(status, gin.H{"error": errMsg})
			return
		}

		if req.Stream {
			handleStreamWithFallback(c, entries, providers, &req, limiter, apiKey)
		} else {
			handleChatWithFallback(c, entries, providers, &req, limiter, apiKey)
		}
	}
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
	c *gin.Context,
	entries []alias.Entry,
	providers map[string]provider.Provider,
	req *models.ChatRequest,
	limiter *ratelimit.Limiter,
	apiKey string,
) {
	ctx := c.Request.Context()
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
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
				return
			}
			log.Printf("reserve error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
			return
		}

		resp, err := p.Chat(ctx, &attempt)
		if err != nil {
			_ = limiter.Commit(ctx, apiKey, token, 0)
			lastErr = err
			if ctx.Err() != nil || !provider.IsRetriable(err) {
				break
			}
			if i < len(entries)-1 {
				log.Printf("alias: attempt %d/%d failed (%v), trying next entry", i+1, len(entries), err)
			}
			continue
		}

		if err := limiter.Commit(ctx, apiKey, token, resp.Usage.TotalTokens); err != nil {
			log.Printf("commit error (non-fatal): %v", err)
		}
		resp.ResolvedProvider = entry.Provider
		c.JSON(http.StatusOK, resp)
		return
	}

	if lastErr == nil {
		lastErr = errors.New("no available provider for this request")
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": lastErr.Error()})
}

func handleStreamWithFallback(
	c *gin.Context,
	entries []alias.Entry,
	providers map[string]provider.Provider,
	req *models.ChatRequest,
	limiter *ratelimit.Limiter,
	apiKey string,
) {
	ctx := c.Request.Context()
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
					c.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
				}
				return
			}
			log.Printf("reserve error: %v", err)
			if !headersSent {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
			}
			return
		}

		ch, err := p.ChatStream(ctx, &attempt)
		if err != nil {
			_ = limiter.Commit(ctx, apiKey, token, 0)
			lastErr = err
			if ctx.Err() != nil || !provider.IsRetriable(err) {
				break
			}
			if i < len(entries)-1 {
				log.Printf("alias: stream attempt %d/%d failed to start (%v), trying next", i+1, len(entries), err)
			}
			continue
		}

		// Read events. SSE headers are sent lazily on the first content delta
		// so we can still fail over if the stream errors before sending anything.
		contentSent := false
		success := false
		retry := false

	eventLoop:
		for {
			select {
			case <-ctx.Done():
				_ = limiter.Commit(ctx, apiKey, token, 0)
				return

			case event, ok := <-ch:
				if !ok {
					// Channel closed without a Done event.
					_ = limiter.Commit(ctx, apiKey, token, 0)
					if !contentSent {
						lastErr = errors.New("stream closed unexpectedly")
						retry = provider.IsRetriable(lastErr)
					}
					break eventLoop
				}

				if event.Done {
					if event.Err != nil && !contentSent {
						// Stream failed before any content — can still fail over.
						_ = limiter.Commit(ctx, apiKey, token, 0)
						lastErr = event.Err
						retry = ctx.Err() == nil && provider.IsRetriable(event.Err)
						if retry && i < len(entries)-1 {
							log.Printf("alias: stream attempt %d/%d failed before content (%v), trying next", i+1, len(entries), event.Err)
						}
						break eventLoop
					}
					actual := 0
					if event.Usage != nil {
						actual = event.Usage.TotalTokens
					}
					if err := limiter.Commit(ctx, apiKey, token, actual); err != nil {
						log.Printf("stream commit (non-fatal): %v", err)
					}
					success = true
					break eventLoop
				}

				// Normal content delta — commit to this stream.
				if !headersSent {
					c.Writer.Header().Set("Content-Type", "text/event-stream")
					c.Writer.Header().Set("Cache-Control", "no-cache")
					c.Writer.Header().Set("Connection", "keep-alive")
					headersSent = true
				}
				contentSent = true
				c.SSEvent("message", event)
				c.Writer.Flush()
			}
		}

		if success {
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
		c.JSON(http.StatusBadGateway, gin.H{"error": lastErr.Error()})
	}
}
