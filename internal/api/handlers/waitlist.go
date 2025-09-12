package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Request/Response types (unchanged)
type JoinWaitlistRequest struct {
	RequestedSeats int32 `json:"requested_seats" binding:"required,min=1"`
}

type JoinWaitlistResponse struct {
	ID       string    `json:"id"`
	Position int64     `json:"position"`
	Created  time.Time `json:"created_at"`
}

// POST /events/:id/waitlist
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
	q := h.db

	eventParam := pgtype.UUID{Bytes: eventID, Valid: true}
	userParam := pgtype.UUID{Bytes: uid, Valid: true}

	row, err := q.InsertWaitlist(ctx, db.InsertWaitlistParams{
		EventID:        eventParam,
		UserID:         userParam,
		RequestedSeats: req.RequestedSeats,
	})
	if err != nil {
		// Try to detect Postgres unique-violation reliably
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				// Duplicate entry -> the user already in waitlist for this event
				c.JSON(http.StatusConflict, gin.H{
					"error":   "already joined waitlist",
					"details": pgErr.Detail,
				})
				return
			}
			// other pg errors -> forward as 500 with some details
			log.Printf("JoinWaitlist: pg error: code=%s message=%s detail=%s", pgErr.Code, pgErr.Message, pgErr.Detail)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join waitlist", "details": pgErr.Message})
			return
		}

		// Fallback: string match (defensive)
		errStr := err.Error()
		if strings.Contains(errStr, "23505") || strings.Contains(strings.ToLower(errStr), "duplicate key") || strings.Contains(strings.ToLower(errStr), "unique constraint") {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "already joined waitlist",
				"details": errStr,
			})
			return
		}

		// Unknown error
		log.Printf("JoinWaitlist: unexpected db error: %T %v", err, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join waitlist", "details": errStr})
		return
	}

	resp := JoinWaitlistResponse{
		ID:       row.ID.String(),
		Position: row.Position,
		Created:  row.CreatedAt.Time,
	}
	c.JSON(http.StatusAccepted, resp)
}
