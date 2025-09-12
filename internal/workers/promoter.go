package workers

import (
	"context"
	"fmt"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WaitlistWorker struct {
	DBConn *pgxpool.Pool
	Pool   *pgxpool.Pool
	DB     *db.Queries
}

func NewWaitlistWorker(conn *pgxpool.Pool) *WaitlistWorker {
	return &WaitlistWorker{
		DBConn: conn,
		DB:     db.New(conn),
	}
}

func NewWaitlistWorkerFromPool(pool *pgxpool.Pool) *WaitlistWorker {
	return &WaitlistWorker{
		Pool: pool,
		DB:   db.New(pool),
	}
}

func (w *WaitlistWorker) ProcessWaitlistForEvent(ctx context.Context, eventID uuid.UUID) error {
	eventParam := pgtype.UUID{Bytes: eventID, Valid: true}

	var waiters []db.GetWaitingListByEventRow
	var err error

	if w.Pool != nil {
		conn, aerr := w.Pool.Acquire(ctx)
		if aerr != nil {
			return fmt.Errorf("acquire conn: %w", aerr)
		}
		defer conn.Release()
		q := db.New(conn.Conn())
		waiters, err = q.GetWaitingListByEvent(ctx, eventParam)
	} else {
		waiters, err = w.DB.GetWaitingListByEvent(ctx, eventParam)
	}
	if err != nil {
		return fmt.Errorf("failed to load waitlist: %w", err)
	}
	if len(waiters) == 0 {
		return nil
	}

	for _, candidate := range waiters {
		n := int32(candidate.RequestedSeats)

		var tx pgx.Tx
		if w.Pool != nil {
			tx, err = w.Pool.BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				return fmt.Errorf("failed to begin tx: %w", err)
			}
		} else {
			tx, err = w.DBConn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("failed to begin tx: %w", err)
			}
		}

		rolledBack := false
		rollbackIfNeeded := func() {
			if !rolledBack {
				_ = tx.Rollback(ctx)
				rolledBack = true
			}
		}

		qtx := db.New(tx)

		seats, err := qtx.GetAvailableSeatsForEventForUpdate(ctx, db.GetAvailableSeatsForEventForUpdateParams{EventID: eventParam, Limit: n})
		if err != nil || int32(len(seats)) < n {
			rollbackIfNeeded()
			if err != nil {
				continue
			}
			continue
		}

		seatIDs := make([]pgtype.UUID, 0, len(seats))
		seatNos := make([]string, 0, len(seats))
		for _, s := range seats {
			seatIDs = append(seatIDs, s.ID)
			seatNos = append(seatNos, s.SeatNo)
		}

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

		if err := qtx.UpdateSeatsToBooked(ctx, db.UpdateSeatsToBookedParams{BookingID: bookingRow.ID, Column2: seatIDs}); err != nil {
			rollbackIfNeeded()
			continue
		}

		rowsAffected, err := qtx.UpdateEventBookedCount(ctx, db.UpdateEventBookedCountParams{BookedCount: int32(len(seatIDs)), ID: eventParam})
		if err != nil {
			rollbackIfNeeded()
			continue
		}
		if rowsAffected == 0 {
			rollbackIfNeeded()
			continue
		}

		if err := qtx.UpdateWaitlistStatus(ctx, db.UpdateWaitlistStatusParams{ID: candidate.ID, Status: "promoted"}); err != nil {
			rollbackIfNeeded()
			continue
		}

		if err := tx.Commit(ctx); err != nil {
			_ = tx.Rollback(ctx)
			continue
		}

		bookingId, perr := uuid.Parse(bookingRow.ID.String())
		if perr == nil {
			go NotifyUserPromoted(candidate.UserID, eventID, bookingId, seatNos)
		}
	}

	return nil
}

func NotifyUserPromoted(userID pgtype.UUID, eventID, bookingID uuid.UUID, seats []string) {
	if userID.Valid {
		fmt.Printf("User %s promoted for event %s (booking %s), seats=%v\n", userID.String(), eventID.String(), bookingID.String(), seats)
	}
}
