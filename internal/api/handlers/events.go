package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

func (h *EventsHandler) GetEvents(c *gin.Context) {
	events, err := h.db.GetAllEvents(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch events",
			"details": err.Error(),
		})
		return
	}

	var response []EventResponse
	for _, event := range events {
		venue := (*string)(nil)
		if event.Venue.Valid {
			venue = &event.Venue.String
		}
		startTime := (*time.Time)(nil)
		if event.StartTime.Valid {
			startTime = &event.StartTime.Time
		}

		response = append(response, EventResponse{
			ID:          event.ID.String(),
			Name:        event.Name,
			Venue:       venue,
			StartTime:   startTime,
			Capacity:    event.Capacity,
			BookedCount: event.BookedCount,
			Metadata:    event.Metadata,
			CreatedAt:   event.CreatedAt.Time,
			UpdatedAt:   event.UpdatedAt.Time,
		})
	}

	c.JSON(http.StatusOK, response)
}

func (h *EventsHandler) GetEventByID(c *gin.Context) {
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid UUID",
			"details": err.Error(),
		})
		return
	}

	// Validate UUID
	event, err := h.db.GetEventByID(context.Background(), pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "Event not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to fetch event",
			"details": err.Error(),
		})
		return
	}

	// Convert to response format
	response := EventResponse{
		ID:          event.ID.String(),
		Name:        event.Name,
		Venue:       (*string)(nil),
		StartTime:   (*time.Time)(nil),
		Capacity:    event.Capacity,
		BookedCount: event.BookedCount,
		Metadata:    event.Metadata,
		CreatedAt:   event.CreatedAt.Time,
		UpdatedAt:   event.UpdatedAt.Time,
	}
	if event.Venue.Valid {
		response.Venue = &event.Venue.String
	}
	if event.StartTime.Valid {
		response.StartTime = &event.StartTime.Time
	}

	c.JSON(http.StatusOK, response)
}
