package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AuthMiddleware validates a JWT from the Authorization header (Bearer token).
// On success it sets "user_id" and "user_role" in the gin.Context.
func AuthMiddleware() gin.HandlerFunc {
	secret := os.Getenv("JWT_SECRET")
	return func(c *gin.Context) {
		if secret == "" {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":   "Server misconfiguration: JWT secret not set",
				"details": "Set JWT_SECRET environment variable",
			})
			return
		}

		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized to perform this action"})
			return
		}

		var tokenString string
		// Accept both "Bearer TOKEN" and "Bearer: TOKEN"
		if strings.HasPrefix(auth, "Bearer ") {
			tokenString = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		} else if strings.HasPrefix(auth, "Bearer:") {
			tokenString = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer:"))
		} else {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header format must be Bearer {token}"})
			return
		}

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Bearer token missing"})
			return
		}

		token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
			// Ensure signing method is HMAC
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
			return
		}

		// Extract sub and role
		var sub, role string
		if v, exists := claims["sub"]; exists && v != nil {
			sub = fmt.Sprintf("%v", v)
		}
		if v, exists := claims["role"]; exists && v != nil {
			role = fmt.Sprintf("%v", v)
		}

		// Store in Gin context
		if sub != "" {
			c.Set("user_id", sub)
		}
		if role != "" {
			c.Set("user_role", role)
		}

		c.Next()
	}
}
