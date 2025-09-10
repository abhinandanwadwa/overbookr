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

	// Event routes
	eventHandler := handlers.NewEventsHandler(deps.DB)
	events := router.Group("/events")
	{
		events.POST("/", middleware.AuthMiddleware(), middleware.AdminMiddleware(), eventHandler.CreateEvent)
		events.GET("/", eventHandler.GetEvents)
		events.GET("/:id", eventHandler.GetEventByID)
	}

	// User routes
	userHandler := handlers.NewUsersHandler(deps.DB)
	users := router.Group("/users")
	{
		users.POST("/register", userHandler.Register)
		users.POST("/login", userHandler.Login)
	}

	seatsHandler := handlers.NewSeatsHandler(deps.DB)
	seats := router.Group("/seats/event/:id")
	{
		seats.GET("/", seatsHandler.GetSeats)
		seats.POST("/", middleware.AuthMiddleware(), middleware.AdminMiddleware(), seatsHandler.BulkCreateSeats)
	}

	return router
}
