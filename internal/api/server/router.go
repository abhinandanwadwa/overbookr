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

	// User routes
	userHandler := handlers.NewUsersHandler(deps.DB)
	users := router.Group("/users")
	{
		users.POST("/register", userHandler.Register)
		users.POST("/login", userHandler.Login)
	}

	// Event routes
	eventHandler := handlers.NewEventsHandler(deps.DB)
	events := router.Group("/events")
	{
		events.POST("/", middleware.AuthMiddleware(), middleware.AdminMiddleware(), eventHandler.CreateEvent)
		events.GET("/", eventHandler.GetEvents)
		events.GET("/:id", eventHandler.GetEventByID)

		// Seats
		events.GET("/:id/seats", eventHandler.GetSeats)
		events.POST("/:id/seats", middleware.AuthMiddleware(), middleware.AdminMiddleware(), eventHandler.BulkCreateSeats)
	}

	holdsHandler := handlers.NewHoldsHandler(deps.DB)
	holds := router.Group("/holds")
	{
		holds.POST("/", middleware.AuthMiddleware(), holdsHandler.CreateHold)
	}

	bookingsHandler := handlers.NewBookingsHandler(deps.DB)
	bookings := router.Group("/bookings")
	{
		bookings.POST("/", middleware.AuthMiddleware(), bookingsHandler.CreateBooking)
		bookings.GET("/", middleware.AuthMiddleware(), bookingsHandler.GetMyBookings)
		bookings.GET("/:id", middleware.AuthMiddleware(), bookingsHandler.GetBookingByID)
		bookings.DELETE("/:id", middleware.AuthMiddleware(), bookingsHandler.CancelBooking)
	}

	return router
}
