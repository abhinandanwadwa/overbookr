package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventsHandler struct {
	db *db.Queries
	DB *pgxpool.Pool
}

type CreateEventRequest struct {
	Name      string          `json:"name" binding:"required"`
	Venue     string          `json:"venue" binding:"required"`
	StartTime time.Time       `json:"start_time" binding:"required"`
	Capacity  int32           `json:"capacity" binding:"required"`
	Metadata  json.RawMessage `json:"metadata"`
}

type CreateEventResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Venue     string          `json:"venue"`
	StartTime time.Time       `json:"start_time"`
	Capacity  int32           `json:"capacity"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type UpdateEventRequest struct {
	Name      *string          `json:"name"`
	Venue     *string          `json:"venue"`
	StartTime *time.Time       `json:"start_time"`
	Capacity  *int32           `json:"capacity"`
	Metadata  *json.RawMessage `json:"metadata"`
}

type EventResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Venue       *string         `json:"venue"`
	StartTime   *time.Time      `json:"start_time"`
	Capacity    int32           `json:"capacity"`
	BookedCount int32           `json:"booked_count"`
	Available   int32           `json:"available"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func NewEventsHandler(dbconn *pgxpool.Pool) *EventsHandler {
	return &EventsHandler{
		db: db.New(dbconn),
		DB: dbconn,
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
	// Defaults
	const (
		defaultLimit  = 20
		defaultOffset = 0
		maxLimit      = 100
	)

	// Parse query params
	limitStr := c.DefaultQuery("limit", strconv.Itoa(defaultLimit))
	offsetStr := c.DefaultQuery("offset", strconv.Itoa(defaultOffset))

	limit64, err := strconv.ParseInt(limitStr, 10, 32)
	if err != nil || limit64 <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid 'limit' query parameter",
			"details": "limit must be a positive integer",
		})
		return
	}
	offset64, err := strconv.ParseInt(offsetStr, 10, 32)
	if err != nil || offset64 < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid 'offset' query parameter",
			"details": "offset must be a non-negative integer",
		})
		return
	}

	// Enforce max limit
	if limit64 > maxLimit {
		limit64 = maxLimit
	}

	// Call the sqlc-generated method
	events, err := h.db.GetAllEvents(context.Background(), db.GetAllEventsParams{Limit: int32(limit64), Offset: int32(offset64)})
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
			Available:   event.Capacity - event.BookedCount,
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
		Available:   event.Capacity - event.BookedCount,
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

func (h *EventsHandler) UpdateEvent(c *gin.Context) {
	idStr := c.Param("id")
	eid, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	var req UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload", "details": err.Error()})
		return
	}

	ctx := context.Background()

	existing, err := h.db.GetEventByID(ctx, pgtype.UUID{Bytes: eid, Valid: true})
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch event", "details": err.Error()})
		return
	}

	// Name: UpdateEventParams.Name is string (non-nullable)
	finalName := existing.Name
	if req.Name != nil {
		finalName = *req.Name
	}

	// Venue: UpdateEventParams.Venue is pgtype.Text (nullable)
	var finalVenue pgtype.Text
	if req.Venue != nil {
		finalVenue = pgtype.Text{String: *req.Venue, Valid: true}
	} else {
		// existing.Venue is pgtype.Text in generated GetEventByIDRow
		finalVenue = existing.Venue
	}

	// StartTime: UpdateEventParams.StartTime is pgtype.Timestamptz
	var finalStart pgtype.Timestamptz
	if req.StartTime != nil {
		finalStart = pgtype.Timestamptz{Time: *req.StartTime, Valid: true}
	} else {
		finalStart = existing.StartTime
	}

	// Capacity: UpdateEventParams.Capacity is int32 (non-nullable)
	finalCapacity := existing.Capacity
	if req.Capacity != nil {
		finalCapacity = *req.Capacity
	}

	// Metadata: UpdateEventParams.Metadata is []byte
	var finalMeta []byte
	if req.Metadata != nil {
		finalMeta = []byte(*req.Metadata)
	} else {
		finalMeta = existing.Metadata
	}

	// 2. Precheck capacity
	if req.Capacity != nil && *req.Capacity < existing.BookedCount {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "capacity cannot be less than booked_count",
			"current": existing.BookedCount,
			"given":   *req.Capacity,
		})
		return
	}

	// Build params in the exact generated types
	params := db.UpdateEventParams{
		ID:        pgtype.UUID{Bytes: eid, Valid: true},
		Name:      finalName,
		Venue:     finalVenue,
		StartTime: finalStart,
		Capacity:  finalCapacity,
		Metadata:  finalMeta,
	}

	// Call UpdateEvent
	updated, err := h.db.UpdateEvent(ctx, params)
	if err != nil {
		// Distinguish "no rows updated" (capacity too small or missing event) vs other errors.
		if err == pgx.ErrNoRows {
			// If event exists but capacity prevented update, return 409 with detail.
			ev, gerr := h.db.GetEventByID(ctx, pgtype.UUID{Bytes: eid, Valid: true})
			if gerr != nil {
				// if event really doesn't exist
				if gerr == pgx.ErrNoRows {
					c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify event", "details": gerr.Error()})
				return
			}
			// event exists -> probably capacity constraint
			c.JSON(http.StatusConflict, gin.H{
				"error":        "capacity too small",
				"booked_count": ev.BookedCount,
				"message":      "new capacity must be >= current booked_count",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update event", "details": err.Error()})
		return
	}

	// Build response using the EventResponse shape
	var venuePtr *string
	if updated.Venue.Valid {
		venuePtr = &updated.Venue.String
	}
	var startPtr *time.Time
	if updated.StartTime.Valid {
		startPtr = &updated.StartTime.Time
	}

	resp := EventResponse{
		ID:          updated.ID.String(),
		Name:        updated.Name,
		Venue:       venuePtr,
		StartTime:   startPtr,
		Capacity:    updated.Capacity,
		BookedCount: updated.BookedCount,
		Metadata:    updated.Metadata,
		CreatedAt:   updated.CreatedAt.Time,
		UpdatedAt:   updated.UpdatedAt.Time,
	}

	c.JSON(http.StatusOK, resp)
}

func (h *EventsHandler) DeleteEvent(c *gin.Context) {
	idStr := c.Param("id")
	eid, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	ctx := context.Background()

	// call sqlc-generated DeleteEvent (expects pgtype.UUID)
	row, err := h.db.DeleteEvent(ctx, pgtype.UUID{Bytes: eid, Valid: true})
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete event", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      row.String(),
		"deleted": true,
	})
}
