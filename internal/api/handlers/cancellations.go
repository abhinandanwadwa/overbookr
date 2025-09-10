package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	workers "github.com/abhinandanwadwa/overbookr/internal/worker"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func EnqueuePromoteEvent(conn *pgx.Conn, eventID uuid.UUID) {
	promoterWorker := workers.NewWaitlistWorker(conn)

	if err := promoterWorker.ProcessWaitlistForEvent(context.Background(), eventID); err != nil {
		fmt.Printf("waitlist promotion failed for event %s: %v\n", eventID, err)
	}
}

// CancelBookingHandler cancels a booking (owner or admin).
// Routes: DELETE /bookings/:id  OR  POST /bookings/:id/cancel
func (h *BookingsHandler) CancelBooking(c *gin.Context) {
	ctx := context.Background()
	bookingIDStr := c.Param("id")
	bookingID, err := uuid.Parse(bookingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid booking id", "details": err.Error()})
		return
	}

	// get current user info from context (set by your auth middleware)
	var currentUserID uuid.UUID
	var currentUserRole string
	if v, ok := c.Get("user_id"); ok {
		switch t := v.(type) {
		case uuid.UUID:
			currentUserID = t
		case string:
			if parsed, perr := uuid.Parse(t); perr == nil {
				currentUserID = parsed
			}
		}
	}
	if r, ok := c.Get("user_role"); ok {
		if s, ok2 := r.(string); ok2 {
			currentUserRole = s
		}
	}

	// Begin transaction
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction", "details": err.Error()})
		return
	}
	// ensure rollback if we exit before commit
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	// 1) Load booking FOR UPDATE
	bookingRow, err := q.GetBookingForUpdate(ctx, pgtype.UUID{Bytes: bookingID, Valid: true})
	if err != nil {
		// if not found
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "booking not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch booking", "details": err.Error()})
		return
	}

	// Authorization: owner or admin
	isOwner := false
	if bookingRow.UserID.Valid {
		if bookingRow.UserID.Bytes == currentUserID {
			isOwner = true
		}
	}
	if !(isOwner || currentUserRole == "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: only booking owner or admin can cancel"})
		return
	}

	// Only cancel if booking is 'active'
	if bookingRow.Status != "active" {
		c.JSON(http.StatusConflict, gin.H{"error": "booking cannot be cancelled", "status": bookingRow.Status})
		return
	}

	// collect seat_ids from bookingRow.SeatIds (pgtype.UUID array)
	seatIDs := make([]pgtype.UUID, 0, len(bookingRow.SeatIds))
	seatIDs = append(seatIDs, bookingRow.SeatIds...)

	// number of seats to decrement
	nSeats := int32(len(seatIDs))
	if nSeats == 0 {
		// nothing to do but still mark booking cancelled
		if err := q.UpdateBookingToCancelled(ctx, pgtype.UUID{Bytes: bookingID, Valid: true}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel booking", "details": err.Error()})
			return
		}
		// commit
		if err := tx.Commit(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit transaction", "details": err.Error()})
			return
		}
		// enqueue promotion job after commit
		go EnqueuePromoteEvent(h.DB, bookingRow.EventID.Bytes)
		c.JSON(http.StatusOK, gin.H{"id": bookingID.String(), "status": "cancelled"})
		return
	}

	// 2) Update booking.status -> 'cancelled'
	if err := q.UpdateBookingToCancelled(ctx, pgtype.UUID{Bytes: bookingID, Valid: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel booking", "details": err.Error()})
		return
	}

	// 3) Update seats -> available
	if err := q.UpdateSeatsToAvailableByIds(ctx, seatIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update seats", "details": err.Error()})
		return
	}

	// 4) Update events.booked_count = booked_count - nSeats
	// Pass negative delta
	if err := q.UpdateEventBookedCountByDelta(ctx, db.UpdateEventBookedCountByDeltaParams{BookedCount: -nSeats, ID: bookingRow.EventID}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update event count", "details": err.Error()})
		return
	}

	// Commit transaction
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit", "details": err.Error()})
		return
	}

	// After commit, enqueue promote job to process waitlist
	go EnqueuePromoteEvent(h.DB, bookingRow.EventID.Bytes)

	c.JSON(http.StatusOK, gin.H{
		"id":     bookingID.String(),
		"status": "cancelled",
	})
}
