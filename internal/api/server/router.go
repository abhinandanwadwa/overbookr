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

	eventHandler := handlers.NewEventsHandler(deps.DB)
	userHandler := handlers.NewUsersHandler(deps.DB)
	events := router.Group("/events")
	{
		events.POST("/", eventHandler.CreateEvent)
		events.GET("/", eventHandler.GetEvents)
	}
	users := router.Group("/users")
	{
		users.POST("/register", userHandler.Register)
		users.POST("/login", userHandler.Login)
	}

	return router
}
