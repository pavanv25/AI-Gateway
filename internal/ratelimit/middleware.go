package ratelimit

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	APIKeyHeader     = "X-API-Key"
	APIKeyContextKey = "apiKey"
)

// AuthMiddleware extracts the X-API-Key header and stores it in the Gin
// context under APIKeyContextKey. Returns 401 if the header is absent.
// No database validation is performed — presence of the key is sufficient.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(APIKeyHeader)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing X-API-Key header",
			})
			return
		}
		c.Set(APIKeyContextKey, key)
		c.Next()
	}
}
