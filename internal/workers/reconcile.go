package workers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReconcileWorker performs periodic consistency checks and optionally fixes mismatches.
type ReconcileWorker struct {
	DBConn *pgxpool.Pool
}

// NewReconcileWorker constructs the worker
func NewReconcileWorker(conn *pgxpool.Pool) *ReconcileWorker {
	return &ReconcileWorker{DBConn: conn}
}

// ReconcileEventsAndSeats runs reconciliation:
// 1) find events where events.booked_count != SUM(active bookings) and fix/log
// 2) find seats with status='booked' but booking_id doesn't exist and fix/log
func (r *ReconcileWorker) Reconcile(ctx context.Context) error {
	if err := r.reconcileEventCounts(ctx); err != nil {
		return fmt.Errorf("reconcile event counts: %w", err)
	}
	if err := r.reconcileOrphanBookedSeats(ctx); err != nil {
		return fmt.Errorf("reconcile orphan seats: %w", err)
	}
	return nil
}

func (r *ReconcileWorker) reconcileEventCounts(ctx context.Context) error {
	rows, err := r.DBConn.Query(ctx, `
		SELECT e.id, e.booked_count, COALESCE(b.cnt,0) AS actual
		FROM events e
		LEFT JOIN (
		  SELECT event_id, SUM(seats) AS cnt
		  FROM bookings
		  WHERE status = 'active'
		  GROUP BY event_id
		) b ON e.id = b.event_id
		WHERE e.booked_count IS DISTINCT FROM COALESCE(b.cnt,0)
	`)
	if err != nil {
		return fmt.Errorf("query mismatch events: %w", err)
	}
	defer rows.Close()

	type row struct {
		EventID     uuid.UUID
		BookedCount int32
		Actual      int64
	}
	var mismatches []row
	for rows.Next() {
		var rrow row
		if err := rows.Scan(&rrow.EventID, &rrow.BookedCount, &rrow.Actual); err != nil {
			return fmt.Errorf("scan mismatch row: %w", err)
		}
		mismatches = append(mismatches, rrow)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows err: %w", err)
	}

	for _, m := range mismatches {
		// Decide whether to auto-fix or log. Here we fix by setting events.booked_count = actual
		_, err := r.DBConn.Exec(ctx, `
			UPDATE events SET booked_count = $1, updated_at = now() WHERE id = $2
		`, m.Actual, m.EventID)
		if err != nil {
			// log and continue
			fmt.Printf("failed to fix event %s: %v\n", m.EventID.String(), err)
			continue
		}
		fmt.Printf("fixed event %s: booked_count %d -> %d\n", m.EventID.String(), m.BookedCount, m.Actual)
	}

	return nil
}

func (r *ReconcileWorker) reconcileOrphanBookedSeats(ctx context.Context) error {
	// find seats that are marked 'booked' but whose booking_id doesn't exist or is not active
	rows, err := r.DBConn.Query(ctx, `
		SELECT s.id, s.event_id
		FROM seats s
		LEFT JOIN bookings b ON s.booking_id = b.id
		WHERE s.status = 'booked' AND (b.id IS NULL OR b.status <> 'active')
	`)
	if err != nil {
		return fmt.Errorf("query orphan seats: %w", err)
	}
	defer rows.Close()

	type orphan struct {
		SeatID  uuid.UUID
		EventID uuid.UUID
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.SeatID, &o.EventID); err != nil {
			return fmt.Errorf("scan orphan row: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows err: %w", err)
	}

	for _, o := range orphans {
		// fix: set seat available and clear booking_id; decrement event booked_count by 1
		tx, err := r.DBConn.Begin(ctx)
		if err != nil {
			fmt.Printf("begin tx for orphan seat %s failed: %v\n", o.SeatID, err)
			continue
		}
		rolledBack := false
		rollback := func() {
			if !rolledBack {
				_ = tx.Rollback(ctx)
				rolledBack = true
			}
		}

		// mark seat available and clear booking id
		if _, err := tx.Exec(ctx, `
			UPDATE seats
			SET status = 'available', booking_id = NULL, updated_at = now()
			WHERE id = $1
		`, o.SeatID); err != nil {
			rollback()
			fmt.Printf("failed to fix seat %s: %v\n", o.SeatID, err)
			continue
		}

		// decrement event booked_count by 1 (best-effort)
		if _, err := tx.Exec(ctx, `
			UPDATE events
			SET booked_count = GREATEST(0, booked_count - 1), updated_at = now()
			WHERE id = $1
		`, o.EventID); err != nil {
			rollback()
			fmt.Printf("failed to decrement event %s: %v\n", o.EventID, err)
			continue
		}

		if err := tx.Commit(ctx); err != nil {
			rollback()
			fmt.Printf("commit failed for orphan seat %s: %v\n", o.SeatID, err)
			continue
		}

		fmt.Printf("fixed orphan seat %s for event %s\n", o.SeatID, o.EventID)
	}

	return nil
}
