package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		dur := time.Since(start)
		method := c.Request.Method
		path := c.Request.URL.Path
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		latency := dur.Milliseconds()

		// Log format: [METHOD] PATH - STATUS - CLIENT_IP - LATENCY ms
		log.Printf("[%s] %s - %d - %s - %d ms", method, path, status, clientIP, latency)
	}
}
