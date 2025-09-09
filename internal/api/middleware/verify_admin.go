package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminMiddleware requires AuthMiddleware to have run earlier (so user_role is set).
// It rejects requests where the user's role is not "admin".
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get("user_role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		role, ok := val.(string)
		if !ok || role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden: admin only"})
			return
		}
		c.Next()
	}
}
