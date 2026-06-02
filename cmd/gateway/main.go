package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/pavanv25/ai-gateway/internal/alias"
	"github.com/pavanv25/ai-gateway/internal/api"
	"github.com/pavanv25/ai-gateway/internal/cache"
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
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		providers["openai"] = provider.NewOpenAIProvider(key)
		log.Printf("openai provider registered")
	} else {
		log.Printf("warn: OPENAI_API_KEY not set — openai provider disabled")
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		providers["anthropic"] = provider.NewAnthropicProvider(key)
		log.Printf("anthropic provider registered")
	} else {
		log.Printf("warn: ANTHROPIC_API_KEY not set — anthropic provider disabled")
	}

	if raw := os.Getenv("CB_FAILURE_THRESHOLD"); raw != "" {
		cbThreshold, err := strconv.Atoi(raw)
		if err != nil || cbThreshold < 0 {
			log.Printf("warn: invalid CB_FAILURE_THRESHOLD %q, circuit breakers disabled", raw)
		} else if cbThreshold > 0 {
			cbCooldown := 60 * time.Second
			if cs := os.Getenv("CB_COOLDOWN_SECONDS"); cs != "" {
				if v, err := strconv.Atoi(cs); err == nil && v > 0 {
					cbCooldown = time.Duration(v) * time.Second
				} else {
					log.Printf("warn: invalid CB_COOLDOWN_SECONDS %q, using default %v", cs, cbCooldown)
				}
			}
			cbCfg := provider.Config{FailureThreshold: cbThreshold, CooldownDuration: cbCooldown}
			for name, p := range providers {
				providers[name] = provider.New(p, cbCfg)
			}
			log.Printf("circuit breakers enabled: threshold=%d cooldown=%v", cbThreshold, cbCooldown)
		}
	} else {
		log.Printf("circuit breakers disabled (CB_FAILURE_THRESHOLD not set)")
	}

	resolver, err := alias.Load(os.Getenv("ALIAS_CONFIG"))
	if err != nil {
		log.Fatalf("alias config: %v", err)
	}
	if resolver != nil {
		log.Printf("alias config loaded from %q", os.Getenv("ALIAS_CONFIG"))
	} else {
		log.Printf("alias feature disabled (ALIAS_CONFIG not set)")
	}

	var semanticCache cache.Cache
	qdrantURL := os.Getenv("QDRANT_URL")
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if qdrantURL != "" && openAIKey != "" {
		cacheTTL := int64(3600)
		if raw := os.Getenv("CACHE_TTL"); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
				cacheTTL = v
			} else {
				log.Printf("warn: invalid CACHE_TTL %q, using default %d", raw, cacheTTL)
			}
		}
		sc, err := cache.New(openAIKey, qdrantURL, os.Getenv("QDRANT_API_KEY"), cacheTTL)
		if err != nil {
			log.Printf("warn: semantic cache disabled — init failed: %v", err)
		} else {
			semanticCache = sc
			log.Printf("semantic cache enabled (qdrant=%s ttl=%ds)", qdrantURL, cacheTTL)
		}
	} else if qdrantURL == "" {
		log.Printf("semantic cache disabled (QDRANT_URL not set)")
	} else {
		log.Printf("semantic cache disabled (OPENAI_API_KEY not set — needed for embeddings)")
	}

	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api.RegisterRoutes(r, limiter, providers, resolver, semanticCache, nil)

	log.Printf("starting gateway on :8080 tpm_limit=%d redis=%s", tpmLimit, redisURL)
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
