package workers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HoldExpiryWorker expires seat_holds that passed their expires_at and frees seats.
type HoldExpiryWorker struct {
	DBConn *pgx.Conn
}

// NewHoldExpiryWorker constructs the worker.
func NewHoldExpiryWorker(conn *pgx.Conn) *HoldExpiryWorker {
	return &HoldExpiryWorker{DBConn: conn}
}

// ExpireHolds looks for active seat_holds with expires_at <= now, expires them and frees seats.
// It runs one short transaction per hold.
func (w *HoldExpiryWorker) ExpireHolds(ctx context.Context) error {
	// 1) fetch holds that have expired
	rows, err := w.DBConn.Query(ctx, `
		SELECT id, hold_token, event_id, seat_ids
		FROM seat_holds
		WHERE expires_at <= now() AND status = 'active'
		ORDER BY created_at
	`)
	if err != nil {
		return fmt.Errorf("failed to query expired holds: %w", err)
	}
	defer rows.Close()

	type holdRow struct {
		ID      uuid.UUID
		Token   string
		EventID uuid.UUID
		SeatIDs []uuid.UUID
	}

	var holds []holdRow
	for rows.Next() {
		var id uuid.UUID
		var token string
		var eventID uuid.UUID
		var seatIDs []uuid.UUID
		if err := rows.Scan(&id, &token, &eventID, &seatIDs); err != nil {
			return fmt.Errorf("scan failed: %w", err)
		}
		holds = append(holds, holdRow{ID: id, Token: token, EventID: eventID, SeatIDs: seatIDs})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}

	// nothing to do
	if len(holds) == 0 {
		return nil
	}

	// Process each hold in its own short transaction
	for _, h := range holds {
		if err := w.processSingleHold(ctx, h); err != nil {
			// Log and continue with next hold
			// Replace with real logger in your app
			fmt.Printf("failed to expire hold %s: %v\n", h.ID.String(), err)
			continue
		}

		// After commit, trigger promote worker for this event (non-blocking)
		go func(ev uuid.UUID) {
			// create a waitlist worker bound to same DB connection and process
			promoter := NewWaitlistWorker(w.DBConn)
			// ignore error but you should log it
			if err := promoter.ProcessWaitlistForEvent(context.Background(), ev); err != nil {
				fmt.Printf("promote failed for event %s: %v\n", ev.String(), err)
			}
		}(h.EventID)
	}

	return nil
}

func (w *HoldExpiryWorker) processSingleHold(ctx context.Context, hRow struct {
	ID      uuid.UUID
	Token   string
	EventID uuid.UUID
	SeatIDs []uuid.UUID
}) error {
	// Start transaction
	tx, err := w.DBConn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// ensure rollback if anything fails
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			_ = tx.Rollback(ctx)
			rolledBack = true
		}
	}

	// lock the seat rows for update
	// we lock by id to avoid potential ambiguity
	_, err = tx.Query(ctx, `SELECT id FROM seats WHERE id = ANY($1::uuid[]) FOR UPDATE`, hRow.SeatIDs)
	if err != nil {
		rollback()
		return fmt.Errorf("select for update seats: %w", err)
	}

	// update seats only if hold_token matches (defensive)
	tag, err := tx.Exec(ctx, `
		UPDATE seats
		SET status = 'available',
		    hold_expires_at = NULL,
		    hold_token = NULL,
		    updated_at = now()
		WHERE hold_token = $1 AND id = ANY($2::uuid[])
	`, hRow.Token, hRow.SeatIDs)
	if err != nil {
		rollback()
		return fmt.Errorf("update seats: %w", err)
	}

	// even if 0 rows changed, we still mark the hold expired (it may have been partially released earlier)
	_ = tag

	// mark the seat_hold as expired
	if _, err := tx.Exec(ctx, `
		UPDATE seat_holds
		SET status = 'expired', updated_at = now()
		WHERE id = $1
	`, hRow.ID); err != nil {
		rollback()
		return fmt.Errorf("update seat_hold status: %w", err)
	}

	// commit
	if err := tx.Commit(ctx); err != nil {
		rollback()
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
