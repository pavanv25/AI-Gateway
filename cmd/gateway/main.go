package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/pavanv25/ai-gateway/internal/api"
	"github.com/pavanv25/ai-gateway/internal/provider"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
)

const defaultTPMLimit = 60_000

func main() {
	tpmLimit := defaultTPMLimit
	if raw := os.Getenv("TPM_LIMIT"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			tpmLimit = v
		} else {
			log.Printf("warn: invalid TPM_LIMIT %q, using default %d", raw, defaultTPMLimit)
		}
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("invalid REDIS_URL %q: %v", redisURL, err)
	}
	rdb := redis.NewClient(opt)

	limiter := ratelimit.New(rdb, ratelimit.Config{TPMLimit: tpmLimit})

	providers := map[string]provider.Provider{
		"mock": provider.NewMockProvider("This is a mock response from the AI gateway."),
	}

	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api.RegisterRoutes(r, limiter, providers)

	log.Printf("starting gateway on :8080 tpm_limit=%d redis=%s", tpmLimit, redisURL)
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
