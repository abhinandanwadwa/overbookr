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

type HoldsHandler struct {
	DB *pgx.Conn
}

type CreateHoldRequest struct {
	EventID string   `json:"event_id" binding:"required,uuid"`
	SeatNos []string `json:"seat_nos" binding:"required,min=1"`
}

type CreateHoldResponse struct {
	HoldToken string    `json:"hold_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

const defaultHoldTTLSeconds = 300 // 5 minutes

func NewHoldsHandler(dbconn *pgx.Conn) *HoldsHandler {
	return &HoldsHandler{
		DB: dbconn,
	}
}

func (h *HoldsHandler) CreateHold(c *gin.Context) {
	var req CreateHoldRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	// Parse event ID
	eid, err := uuid.Parse(req.EventID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	// dedupe seat nos from request to avoid duplicate checks
	seatMap := make(map[string]struct{}, len(req.SeatNos))
	seatNos := make([]string, 0, len(req.SeatNos))
	for _, s := range req.SeatNos {
		if s == "" {
			continue
		}
		if _, ok := seatMap[s]; !ok {
			seatMap[s] = struct{}{}
			seatNos = append(seatNos, s)
		}
	}
	if len(seatNos) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid seat numbers provided"})
		return
	}

	ctx := context.Background()

	// Begin transaction here
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction", "details": err.Error()})
		return
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Create a new queries instance using tx
	q := db.New(tx)
	eventParam := pgtype.UUID{Bytes: eid, Valid: true}

	seats, err := q.GetSeatsForEventForUpdate(ctx, db.GetSeatsForEventForUpdateParams{EventID: eventParam, Column2: seatNos})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get seats", "details": err.Error()})
		return
	}

	// Validate and check that all the requested seats are available
	if len(seats) != len(seatNos) {
		// Compute missing ones
		found := map[string]struct{}{}
		for _, s := range seats {
			found[s.SeatNo] = struct{}{}
		}
		missing := []string{}
		for _, s := range seatNos {
			if _, ok := found[s]; !ok {
				missing = append(missing, s)
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "some seats not found", "details": missing})
		return
	}

	for _, s := range seats {
		if s.Status != "available" {
			c.JSON(http.StatusConflict, gin.H{"error": "one or more seats are not available", "seat_no": s.SeatNo, "status": s.Status})
			return
		}
	}

	// If all seats are valid and available, create a hold now
	ids := make([]pgtype.UUID, 0, len(seats))
	for _, s := range seats {
		ids = append(ids, s.ID)
	}

	// Prepare hold token and expiry
	token := uuid.NewString()
	expiresAt := time.Now().Add(time.Duration(defaultHoldTTLSeconds) * time.Second)

	holdExpiresParam := pgtype.Timestamptz{Time: expiresAt, Valid: true}
	holdTokenParam := pgtype.Text{String: token, Valid: true}
	// Update the seats to be held
	if err := q.UpdateSeatsToHeld(ctx, db.UpdateSeatsToHeldParams{
		HoldExpiresAt: holdExpiresParam,
		HoldToken:     holdTokenParam,
		Column3:       ids, // ids is []pgtype.UUID (from change #1)
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update seats to held", "details": err.Error()})
		return
	}

	// Insert seat_hold via sqlc
	// Get user id from context (set by auth middleware)
	var userIDParam pgtype.UUID
	if uidVal, ok := c.Get("user_id"); ok {
		switch v := uidVal.(type) {
		case uuid.UUID:
			userIDParam = pgtype.UUID{Bytes: v, Valid: true}
		case string:
			if parsed, perr := uuid.Parse(v); perr == nil {
				userIDParam = pgtype.UUID{Bytes: parsed, Valid: true}
			}
		}
	}

	holdRow, err := q.InsertSeatHold(ctx, db.InsertSeatHoldParams{
		HoldToken: token,
		EventID:   eventParam,
		UserID:    userIDParam,
		SeatIds:   ids,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create seat_hold", "details": err.Error()})
		return
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit", "details": err.Error()})
		return
	}

	// return hold token + expiry
	resp := CreateHoldResponse{
		HoldToken: holdRow.HoldToken,
		ExpiresAt: holdRow.ExpiresAt.Time,
	}
	c.JSON(http.StatusCreated, resp)
}
