package workers

import (
	"context"
	"fmt"
	"sync"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HoldExpiryWorker expires seat_holds that passed their expires_at and frees seats.
type HoldExpiryWorker struct {
	Pool *pgxpool.Pool
}

// NewHoldExpiryWorker constructs the worker.
func NewHoldExpiryWorker(pool *pgxpool.Pool) *HoldExpiryWorker {
	return &HoldExpiryWorker{Pool: pool}
}

// ExpireHolds looks for active seat_holds with expires_at <= now, expires them and frees seats.
// It runs one short transaction per hold.
func (w *HoldExpiryWorker) ExpireHolds(ctx context.Context) error {
	// simple log line for observability
	fmt.Println("HoldExpiryWorker: checking for expired holds...")

	// Use the pool to query expired holds (non-transactional read)
	rows, err := w.Pool.Query(ctx, `
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

	if len(holds) == 0 {
		return nil
	}

	// Keep track of events that need waitlist processing
	eventsToPromote := make(map[uuid.UUID]bool)
	var mu sync.Mutex

	// Process each hold in its own short transaction.
	for _, h := range holds {
		if err := w.processSingleHold(ctx, h.ID, h.Token, h.EventID, h.SeatIDs); err != nil {
			// log and continue; don't fail the entire loop for one bad hold
			fmt.Printf("failed to expire hold %s: %v\n", h.ID.String(), err)
			continue
		}

		// Track events that need promotion (deduplicated)
		mu.Lock()
		eventsToPromote[h.EventID] = true
		mu.Unlock()
	}

	// After all holds are processed, trigger promotion for affected events
	// Do this sequentially to avoid connection conflicts
	for eventID := range eventsToPromote {
		if err := w.processWaitlistForEvent(ctx, eventID); err != nil {
			fmt.Printf("promote failed for event %s: %v\n", eventID.String(), err)
		}
	}

	return nil
}

func (w *HoldExpiryWorker) processSingleHold(ctx context.Context, holdID uuid.UUID, token string, eventID uuid.UUID, seatIDs []uuid.UUID) error {
	// Begin a transaction using the pool (this acquires a connection from the pool)
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			_ = tx.Rollback(ctx)
			rolledBack = true
		}
	}
	defer rollback() // Ensure rollback if not committed

	// Wrap tx with sqlc queries (db.New expects a pgx.Tx or compatible)
	q := db.New(tx)

	// Lock seats by id FOR UPDATE to avoid races with other transactions
	lockRows, err := tx.Query(ctx, `SELECT id FROM seats WHERE id = ANY($1::uuid[]) FOR UPDATE`, seatIDs)
	if err != nil {
		return fmt.Errorf("select for update seats: %w", err)
	}
	lockRows.Close() // Close the rows immediately since we only need the lock

	// Convert seatIDs from []uuid.UUID to []pgtype.UUID
	pgSeatIDs := make([]pgtype.UUID, len(seatIDs))
	for i, id := range seatIDs {
		pgSeatIDs[i] = pgtype.UUID{Bytes: id, Valid: true}
	}

	// Update seats only if hold_token matches (defensive)
	if err := q.UpdateSeatsToAvailableByHold(ctx, db.UpdateSeatsToAvailableByHoldParams{
		HoldToken: pgtype.Text{String: token, Valid: true},
		Column2:   pgSeatIDs,
	}); err != nil {
		return fmt.Errorf("update seats: %w", err)
	}

	// Mark the seat_hold as expired
	pgHoldID := pgtype.UUID{Bytes: holdID, Valid: true}
	if err := q.MarkSeatHoldExpired(ctx, pgHoldID); err != nil {
		return fmt.Errorf("update seat_hold status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	rolledBack = true // Mark as committed so defer won't rollback
	return nil
}

// processWaitlistForEvent handles waitlist promotion for a single event
func (w *HoldExpiryWorker) processWaitlistForEvent(ctx context.Context, eventID uuid.UUID) error {
	// Create a waitlist worker bound to the same pool
	promoter := NewWaitlistWorkerFromPool(w.Pool)
	return promoter.ProcessWaitlistForEvent(ctx, eventID)
}
