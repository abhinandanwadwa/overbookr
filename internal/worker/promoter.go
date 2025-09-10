package workers

import (
	"context"
	"fmt"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// WaitlistWorker provides worker methods for waitlist promotion
type WaitlistWorker struct {
	DBConn *pgx.Conn   // used for tx
	DB     *db.Queries // plain queries
}

func NewWaitlistWorker(conn *pgx.Conn) *WaitlistWorker {
	return &WaitlistWorker{
		DBConn: conn,
		DB:     db.New(conn),
	}
}

// ProcessWaitlistForEvent promotes waitlisted users if seats are available.
func (w *WaitlistWorker) ProcessWaitlistForEvent(ctx context.Context, eventID uuid.UUID) error {
	eventParam := pgtype.UUID{Bytes: eventID, Valid: true}

	// 1. Fetch waiting users
	waiters, err := w.DB.GetWaitingListByEvent(ctx, eventParam)
	if err != nil {
		return fmt.Errorf("failed to load waitlist: %w", err)
	}
	if len(waiters) == 0 {
		return nil
	}

	for _, candidate := range waiters {
		n := int32(candidate.RequestedSeats)

		// Begin short transaction
		tx, err := w.DBConn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin tx: %w", err)
		}
		rolledBack := false
		rollbackIfNeeded := func() {
			if !rolledBack {
				_ = tx.Rollback(ctx)
				rolledBack = true
			}
		}

		qtx := db.New(tx)

		// 2. Lock N available seats
		seats, err := qtx.GetAvailableSeatsForEventForUpdate(ctx, db.GetAvailableSeatsForEventForUpdateParams{EventID: eventParam, Limit: n})
		if err != nil || int32(len(seats)) < n {
			rollbackIfNeeded()
			continue
		}

		seatIDs := make([]pgtype.UUID, 0, len(seats))
		seatNos := make([]string, 0, len(seats))
		for _, s := range seats {
			seatIDs = append(seatIDs, s.ID)
			seatNos = append(seatNos, s.SeatNo)
		}

		// 3. Insert booking
		status := "active"
		idempotencyKey := uuid.NewString()
		bookingRow, err := qtx.InsertBooking(ctx,
			db.InsertBookingParams{
				EventID:        eventParam,
				UserID:         candidate.UserID,
				Seats:          int32(len(seatIDs)),
				SeatIds:        seatIDs,
				Status:         status,
				IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
			})
		if err != nil {
			rollbackIfNeeded()
			continue
		}

		// 4. Update seats to booked
		if err := qtx.UpdateSeatsToBooked(ctx, db.UpdateSeatsToBookedParams{BookingID: bookingRow.ID, Column2: seatIDs}); err != nil {
			rollbackIfNeeded()
			continue
		}

		// 5. Update event booked_count
		if err := qtx.UpdateEventBookedCount(ctx, db.UpdateEventBookedCountParams{BookedCount: int32(len(seatIDs)), ID: eventParam}); err != nil {
			rollbackIfNeeded()
			continue
		}

		// 6. Update waitlist row to promoted
		if err := qtx.UpdateWaitlistStatus(ctx, db.UpdateWaitlistStatusParams{ID: candidate.ID, Status: "promoted"}); err != nil {
			rollbackIfNeeded()
			continue
		}

		// Commit
		if err := tx.Commit(ctx); err != nil {
			_ = tx.Rollback(ctx)
			continue
		}

		// Notify user outside transaction
		bookingId, err := uuid.Parse(bookingRow.ID.String())
		if err == nil {
			go NotifyUserPromoted(candidate.UserID, eventID, bookingId, seatNos)
		}
	}

	return nil
}

// Replace this with your real notifier (email, WhatsApp, push, etc.)
func NotifyUserPromoted(userID pgtype.UUID, eventID, bookingID uuid.UUID, seats []string) {
	if userID.Valid {
		fmt.Printf("User %s promoted for event %s (booking %s), seats=%v\n",
			userID.String(), eventID.String(), bookingID.String(), seats)
	}
}
