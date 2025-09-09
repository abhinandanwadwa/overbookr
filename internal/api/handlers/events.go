package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type EventsHandler struct {
	db *db.Queries
}

type CreateEventRequest struct {
	Name      string    `json:"name" binding:"required"`
	Venue     string    `json:"venue" binding:"required"`
	StartTime time.Time `json:"start_time" binding:"required"`
	Capacity  int32     `json:"capacity" binding:"required"`
	Metadata  []byte    `json:"metadata"`
}

type CreateEventResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Venue     string    `json:"venue"`
	StartTime time.Time `json:"start_time"`
	Capacity  int32     `json:"capacity"`
	Metadata  []byte    `json:"metadata"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type EventResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Venue       *string    `json:"venue"`
	StartTime   *time.Time `json:"start_time"`
	Capacity    int32      `json:"capacity"`
	BookedCount int32      `json:"booked_count"`
	Metadata    []byte     `json:"metadata"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func NewEventsHandler(dbconn *pgx.Conn) *EventsHandler {
	return &EventsHandler{
		db: db.New(dbconn),
	}
}

func (h *EventsHandler) CreateEvent(c *gin.Context) {
	var req CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid input",
			"details": err.Error(),
		})
		return
	}

	venue := pgtype.Text{String: req.Venue, Valid: true}
	startTime := pgtype.Timestamptz{Time: req.StartTime, Valid: true}

	params := db.AddEventParams{
		Name:      req.Name,
		Venue:     venue,
		StartTime: startTime,
		Capacity:  req.Capacity,
		Metadata:  req.Metadata,
	}

	// Call the database
	event, err := h.db.AddEvent(context.Background(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to create event",
			"details": err.Error(),
		})
		return
	}

	// Convert to response format
	response := CreateEventResponse{
		ID:        event.ID.String(),
		Name:      event.Name,
		Venue:     venue.String,
		StartTime: startTime.Time,
		Capacity:  event.Capacity,
		Metadata:  event.Metadata,
		CreatedAt: event.CreatedAt.Time,
		UpdatedAt: event.UpdatedAt.Time,
	}

	c.JSON(http.StatusCreated, response)
}
