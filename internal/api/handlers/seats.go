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

type SeatResponse struct {
	SeatNo    string    `json:"seat_no"`
	Status    string    `json:"status"`
	BookingID *string   `json:"booking_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BulkCreateSeatsRequest struct {
	SeatNos []string `json:"seat_nos" binding:"required,min=1"`
}

type SeatsHandler struct {
	db *db.Queries
}

func NewSeatsHandler(dbconn *pgx.Conn) *SeatsHandler {
	return &SeatsHandler{
		db: db.New(dbconn),
	}
}

func (h *SeatsHandler) GetSeats(c *gin.Context) {
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	seats, err := h.db.GetSeatsByEvent(context.Background(), pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch seats", "details": err.Error()})
		return
	}

	resp := make([]SeatResponse, 0, len(seats))
	for _, s := range seats {
		var bid *string
		if s.BookingID.Valid {
			bs := s.BookingID.String()
			bid = &bs
		}

		resp = append(resp, SeatResponse{
			SeatNo:    s.SeatNo,
			Status:    s.Status,
			BookingID: bid,
			CreatedAt: s.CreatedAt.Time,
			UpdatedAt: s.UpdatedAt.Time,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func (h *SeatsHandler) BulkCreateSeats(c *gin.Context) {
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id", "details": err.Error()})
		return
	}

	var req BulkCreateSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body", "details": err.Error()})
		return
	}

	// simple guard: don't allow huge batches
	if len(req.SeatNos) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many seats in a single request", "details": "max 2000"})
		return
	}

	inserted, err := h.db.BulkInsertSeats(context.Background(), db.BulkInsertSeatsParams{EventID: pgtype.UUID{Bytes: uid, Valid: true}, Column2: req.SeatNos})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create seats", "details": err.Error()})
		return
	}

	// Return list of created (or existing) seats
	exResp := make([]SeatResponse, 0, len(inserted))
	for _, s := range inserted {
		var bid *string
		if s.BookingID.Valid {
			bs := s.BookingID.String()
			bid = &bs
		}
		exResp = append(exResp, SeatResponse{
			SeatNo:    s.SeatNo,
			Status:    s.Status,
			BookingID: bid,
			CreatedAt: s.CreatedAt.Time,
			UpdatedAt: s.UpdatedAt.Time,
		})
	}

	c.JSON(http.StatusCreated, exResp)
}
