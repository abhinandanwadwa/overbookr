package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Request/Response types
type JoinWaitlistRequest struct {
	RequestedSeats int32 `json:"requested_seats" binding:"required,min=1"`
}

type JoinWaitlistResponse struct {
	ID       string    `json:"id"`
	Position int64     `json:"position"`
	Created  time.Time `json:"created_at"`
}

// POST /events/:id/waitlist
// Auth required
func (h *EventsHandler) JoinWaitlist(c *gin.Context) {
	var req JoinWaitlistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	eventIDStr := c.Param("id")
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	// get authenticated user id
	var uid uuid.UUID
	if v, ok := c.Get("user_id"); ok {
		switch t := v.(type) {
		case uuid.UUID:
			uid = t
		case string:
			if parsed, perr := uuid.Parse(t); perr == nil {
				uid = parsed
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id in context"})
				return
			}
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id in context"})
			return
		}
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	ctx := context.Background()
	// Use a tx if you prefer; sql above computes position in INSERT atomically so tx isn't required.
	q := h.db

	// prepare params as pgtype where required
	eventParam := pgtype.UUID{Bytes: eventID, Valid: true}
	userParam := pgtype.UUID{Bytes: uid, Valid: true}

	row, err := q.InsertWaitlist(ctx, db.InsertWaitlistParams{EventID: eventParam, UserID: userParam, RequestedSeats: req.RequestedSeats})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join waitlist", "details": err.Error()})
		return
	}

	resp := JoinWaitlistResponse{
		ID:       row.ID.String(),
		Position: row.Position,
		Created:  row.CreatedAt.Time,
	}
	// 202 Accepted as requested
	c.JSON(http.StatusAccepted, resp)
}
