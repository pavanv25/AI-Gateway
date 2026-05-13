package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

// RegisterRoutes wires all application routes onto r.
// limiter and providers are injected — no global state.
func RegisterRoutes(r *gin.Engine, limiter *ratelimit.Limiter, providers map[string]provider.Provider) {
	v1 := r.Group("/v1")
	v1.Use(ratelimit.AuthMiddleware())
	{
		v1.POST("/chat", chatHandler(limiter, providers))
	}
}

func chatHandler(limiter *ratelimit.Limiter, providers map[string]provider.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req models.ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		apiKey := c.GetString(ratelimit.APIKeyContextKey)

		p, ok := providers[req.Provider]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown provider %q", req.Provider)})
			return
		}

		token, err := limiter.Reserve(c.Request.Context(), apiKey, req.MaxTokens)
		if err != nil {
			if errors.Is(err, ratelimit.ErrLimitExceeded) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "token rate limit exceeded"})
				return
			}
			log.Printf("reserve error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rate limiter unavailable"})
			return
		}

		if req.Stream {
			handleStream(c, p, &req, limiter, apiKey, token)
		} else {
			handleChat(c, p, &req, limiter, apiKey, token)
		}
	}
}

func handleChat(
	c *gin.Context, p provider.Provider, req *models.ChatRequest,
	limiter *ratelimit.Limiter, apiKey, token string,
) {
	resp, err := p.Chat(c.Request.Context(), req)
	if err != nil {
		_ = limiter.Commit(c.Request.Context(), apiKey, token, 0)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if err := limiter.Commit(c.Request.Context(), apiKey, token, resp.Usage.TotalTokens); err != nil {
		log.Printf("commit error (non-fatal): %v", err)
	}
	c.JSON(http.StatusOK, resp)
}

func handleStream(
	c *gin.Context, p provider.Provider, req *models.ChatRequest,
	limiter *ratelimit.Limiter, apiKey, token string,
) {
	ch, err := p.ChatStream(c.Request.Context(), req)
	if err != nil {
		_ = limiter.Commit(c.Request.Context(), apiKey, token, 0)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			_ = limiter.Commit(ctx, apiKey, token, 0)
			return
		case event, ok := <-ch:
			if !ok {
				_ = limiter.Commit(ctx, apiKey, token, 0)
				return
			}
			if event.Done {
				actual := 0
				if event.Usage != nil {
					actual = event.Usage.TotalTokens
				}
				if err := limiter.Commit(ctx, apiKey, token, actual); err != nil {
					log.Printf("stream commit (non-fatal): %v", err)
				}
				return
			}
			c.SSEvent("message", event)
			c.Writer.Flush()
		}
	}
}
