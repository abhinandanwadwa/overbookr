package server

import (
	"github.com/abhinandanwadwa/overbookr/internal/api/handlers"
	"github.com/abhinandanwadwa/overbookr/internal/api/middleware"
	"github.com/gin-gonic/gin"
)

type Config struct {
	DB_URI string
	PORT   string
}

func NewRouter(deps AppDeps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.RequestLogger())

	// Public routes
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// eventHander := handlers.NewEventsHandler(deps.DB)
	// eventHandler := handlers.NewEventsHandler()
	eventHandler := handlers.NewEventsHandler(deps.DB)
	api := router.Group("/events")
	{
		api.POST("/", eventHandler.CreateEvent)
	}

	return router
}
