package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type BookingsHandler struct {
	db *db.Queries
	DB *pgx.Conn
}

type CreateBookingRequest struct {
	EventID   string   `json:"event_id" binding:"required,uuid"`
	SeatNos   []string `json:"seat_nos" binding:"required,min=1"`
	HoldToken *string  `json:"hold_token,omitempty"`
}

type CreateBookingResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	Seats     []string  `json:"seat_nos"`
	CreatedAt time.Time `json:"created_at"`
}

// BookingResponse is the shape returned to clients for bookings.
type BookingResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	SeatNos   []string  `json:"seat_nos"`
	SeatIds   []string  `json:"seat_ids,omitempty"` // optional, stringified UUIDs
	SeatsCnt  int32     `json:"seats_count"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Variables for retries
const (
	createBookingMaxRetries = 3
	initialBackoff          = 100 * time.Millisecond
)

func NewBookingsHandler(dbconn *pgx.Conn) *BookingsHandler {
	return &BookingsHandler{
		db: db.New(dbconn),
		DB: dbconn,
	}
}

func (h *BookingsHandler) CreateBooking(c *gin.Context) {
	// Fetch Idempotency header
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key header required"})
		return
	}

	var req CreateBookingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	// parse event id
	eid, err := uuid.Parse(req.EventID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event_id", "details": err.Error()})
		return
	}

	// dedupe & sanitize seat nos
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "seat_nos must contain at least one seat"})
		return
	}

	ctx := context.Background()

	// Before actually performing the booking, return existing booking for same event+idempotency key (if any)
	eventParam := pgtype.UUID{Bytes: eid, Valid: true}
	idempotencyParam := pgtype.Text{String: idempotencyKey, Valid: true}

	existing, err := h.db.GetBookingByEventAndIdempotency(ctx, db.GetBookingByEventAndIdempotencyParams{
		EventID:        eventParam,
		IdempotencyKey: idempotencyParam,
	})
	if err == nil && existing.ID.Bytes != uuid.Nil {
		// Build plain []uuid.UUID from existing.SeatIds (which are pgtype.UUID)
		bookedIDs := make([]uuid.UUID, 0, len(existing.SeatIds))
		for _, pgid := range existing.SeatIds {
			if pgid.Valid {
				bookedIDs = append(bookedIDs, pgid.Bytes)
			}
		}

		// Query seat_no for those ids
		rows, qerr := h.DB.Query(ctx, `SELECT seat_no FROM seats WHERE id = ANY($1::uuid[])`, bookedIDs)
		if qerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load existing booking seats", "details": qerr.Error()})
			return
		}
		defer rows.Close()

		bookedSeatNos := make([]string, 0, len(bookedIDs))
		for rows.Next() {
			var sn string
			if err := rows.Scan(&sn); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read booked seat rows", "details": err.Error()})
				return
			}
			bookedSeatNos = append(bookedSeatNos, sn)
		}

		// Compare sets (order-insensitive). Normalize requested seatNos (dedupe) first.
		reqSet := make(map[string]struct{}, len(req.SeatNos))
		for _, s := range req.SeatNos {
			reqSet[s] = struct{}{}
		}
		bookedSet := make(map[string]struct{}, len(bookedSeatNos))
		for _, s := range bookedSeatNos {
			bookedSet[s] = struct{}{}
		}

		// If sets equal => idempotent retry of same request -> return existing booking
		if len(reqSet) == len(bookedSet) {
			same := true
			for s := range reqSet {
				if _, ok := bookedSet[s]; !ok {
					same = false
					break
				}
			}
			if same {
				resp := CreateBookingResponse{
					ID:        existing.ID.String(),
					EventID:   existing.EventID.String(),
					Seats:     bookedSeatNos, // return canonical booked seats
					CreatedAt: existing.CreatedAt.Time,
				}
				c.JSON(http.StatusOK, resp)
				return
			}
		}

		// Otherwise payload differs -> idempotency collision
		c.JSON(http.StatusConflict, gin.H{
			"error":   "idempotency key conflict: existing booking was created with different seats",
			"booking": map[string]interface{}{"id": existing.ID.String(), "seat_nos": bookedSeatNos},
		})
		return
	}
	// if error is pgx.ErrNoRows (or similar), continue; if other error, fail
	// sqlc may return pgx.ErrNoRows or a zero-value; treat any non-nil error that's not a no-rows as a server error
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pre-check failed", "details": err.Error()})
		return
	}

	// Get authenticated user id if available (auth middleware should set "user_id")
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
	// if not set, userIDParam.Valid == false and sqlc/pgx will insert NULL

	// Retry loop for serialization / deadlock errors
	backoff := initialBackoff
	for attempt := 0; attempt < createBookingMaxRetries; attempt++ {
		// Begin transaction
		tx, err := h.DB.Begin(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction", "details": err.Error()})
			return
		}

		// ensure rollback if anything fails in this attempt
		rolledBack := false
		rollbackIfNeeded := func() {
			if !rolledBack {
				_ = tx.Rollback(ctx)
				rolledBack = true
			}
		}

		// create sqlc queries bound to tx
		q := db.New(tx)

		// 1) Lock requested seats FOR UPDATE (ordered by id)
		seats, err := q.GetSeatsForBookingForUpdate(ctx, db.GetSeatsForBookingForUpdateParams{EventID: eventParam, Column2: seatNos})
		if err != nil {
			rollbackIfNeeded()
			// check serialization/locking errors - retry if needed
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					// serialization_failure or deadlock_detected -> retry
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query seats", "details": err.Error()})
			return
		}

		// Validate we found all seats
		if len(seats) != len(seatNos) {
			// compute missing seats
			found := map[string]struct{}{}
			for _, s := range seats {
				found[s.SeatNo] = struct{}{}
			}
			missing := []string{}
			for _, sn := range seatNos {
				if _, ok := found[sn]; !ok {
					missing = append(missing, sn)
				}
			}
			rollbackIfNeeded()
			c.JSON(http.StatusNotFound, gin.H{"error": "some seats not found for this event", "missing": missing})
			return
		}

		// Validate statuses: must be:
		// - held AND must match provided hold_token
		for _, s := range seats {
			switch s.Status {
			case "available":
				if req.HoldToken != nil {
					// if client provided a hold_token, they can't mix in random available seats
					rollbackIfNeeded()
					c.JSON(http.StatusConflict, gin.H{
						"error":   "cannot book seats outside the provided hold",
						"seat_no": s.SeatNo,
					})
					return
				}
			case "held":
				if req.HoldToken == nil {
					rollbackIfNeeded()
					c.JSON(http.StatusConflict, gin.H{
						"error":   "seat is held, hold_token required",
						"seat_no": s.SeatNo,
					})
					return
				}
				if !s.HoldToken.Valid || s.HoldToken.String != *req.HoldToken {
					rollbackIfNeeded()
					c.JSON(http.StatusConflict, gin.H{
						"error":   "seat held by another hold_token",
						"seat_no": s.SeatNo,
					})
					return
				}
			default:
				rollbackIfNeeded()
				c.JSON(http.StatusConflict, gin.H{
					"error":   "seat not available for booking",
					"seat_no": s.SeatNo,
					"status":  s.Status,
				})
				return
			}
		}

		// Prepare seat IDs as []pgtype.UUID (matches sqlc pgtype usage)
		seatIDs := make([]pgtype.UUID, 0, len(seats))
		for _, s := range seats {
			seatIDs = append(seatIDs, s.ID)
		}

		// 2) Insert booking row (idempotency key included)
		// convert seats count
		seatsCount := int32(len(seatIDs))
		status := "active" // or 'confirmed' depending on your semantics

		bookingRow, err := q.InsertBooking(ctx,
			db.InsertBookingParams{
				EventID:        eventParam,
				UserID:         userIDParam,
				Seats:          seatsCount,
				SeatIds:        seatIDs,
				Status:         status,
				IdempotencyKey: idempotencyParam,
			},
		)
		if err != nil {
			rollbackIfNeeded()
			// retry on serialization/deadlock
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create booking", "details": err.Error()})
			return
		}

		// 3) Update seats to booked
		if err := q.UpdateSeatsToBooked(ctx, db.UpdateSeatsToBookedParams{BookingID: bookingRow.ID, Column2: seatIDs}); err != nil {
			rollbackIfNeeded()
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update seats", "details": err.Error()})
			return
		}

		// 4) Update events booked_count
		if err := q.UpdateEventBookedCount(ctx, db.UpdateEventBookedCountParams{BookedCount: seatsCount, ID: eventParam}); err != nil {
			rollbackIfNeeded()
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update event booked_count", "details": err.Error()})
			return
		}

		// 5) If a hold_token was used, mark that seat_hold as converted
		if req.HoldToken != nil {
			if err := q.ConvertSeatHoldToConverted(ctx, *req.HoldToken); err != nil {
				// Non-fatal? We treat failure as an error that rolls back
				rollbackIfNeeded()
				if pgErr, ok := err.(*pgconn.PgError); ok {
					if pgErr.Code == "40001" || pgErr.Code == "40P01" {
						time.Sleep(backoff)
						backoff *= 2
						continue
					}
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update seat_hold status", "details": err.Error()})
				return
			}
		}

		// Commit
		if err := tx.Commit(ctx); err != nil {
			// retry on serialization failure / deadlock
			_ = tx.Rollback(ctx)
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					// continue to next attempt
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit transaction", "details": err.Error()})
			return
		}

		// Success -> return booking id/details (201)
		resp := CreateBookingResponse{
			ID:        bookingRow.ID.String(),
			EventID:   bookingRow.EventID.String(),
			Seats:     seatNos,
			CreatedAt: bookingRow.CreatedAt.Time,
		}
		c.JSON(http.StatusCreated, resp)
		return
	}

	// if we've exhausted retries
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not complete booking due to concurrent conflicts; please retry"})
}

// GET /bookings/me  -> GetMyBookings
// Returns all bookings that belong to the authenticated user (past + present).
func (h *BookingsHandler) GetMyBookings(c *gin.Context) {
	ctx := context.Background()

	// get authenticated user id (auth middleware must set "user_id")
	var uid uuid.UUID
	if v, ok := c.Get("user_id"); ok {
		switch t := v.(type) {
		case uuid.UUID:
			uid = t
		case string:
			if parsed, err := uuid.Parse(t); err == nil {
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

	// Call sqlc-generated query: GetBookingsByUser(ctx, pgtype.UUID{Bytes: uid, Valid:true})
	userParam := pgtype.UUID{Bytes: uid, Valid: true}
	bookings, err := h.db.GetBookingsByUser(ctx, userParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch bookings", "details": err.Error()})
		return
	}

	out := make([]BookingResponse, 0, len(bookings))
	for _, b := range bookings {
		// build seatIds slice for GetSeatNosByIds query and response
		pgIDs := make([]pgtype.UUID, 0, len(b.SeatIds))
		seatIDStrs := make([]string, 0, len(b.SeatIds))
		for _, pgid := range b.SeatIds {
			pgIDs = append(pgIDs, pgid)
			if pgid.Valid {
				seatIDStrs = append(seatIDStrs, pgid.String())
			}
		}

		// fetch seat numbers for these ids using the GetSeatNosByIds query
		seatNos := []string{}
		if len(pgIDs) > 0 {
			rows, err := h.db.GetSeatNosByIds(ctx, pgIDs)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load seat numbers", "details": err.Error()})
				return
			}
			// for _, s := range rows {
			// 	seatNos = append(seatNos, s)
			// }
			seatNos = append(seatNos, rows...)
		}

		out = append(out, BookingResponse{
			ID:        b.ID.String(),
			EventID:   b.EventID.String(),
			SeatNos:   seatNos,
			SeatIds:   seatIDStrs,
			SeatsCnt:  b.Seats,
			Status:    b.Status,
			CreatedAt: b.CreatedAt.Time,
			UpdatedAt: b.UpdatedAt.Time,
		})
	}

	c.JSON(http.StatusOK, out)
}

// GET /bookings/:id -> GetBookingByID (only owner can view)
func (h *BookingsHandler) GetBookingByID(c *gin.Context) {
	ctx := context.Background()
	bookingIDStr := c.Param("id")
	bookingID, err := uuid.Parse(bookingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid booking id", "details": err.Error()})
		return
	}

	// fetch the booking
	b, err := h.db.GetBookingByID(ctx, pgtype.UUID{Bytes: bookingID, Valid: true})
	if err != nil {
		// not found or other
		c.JSON(http.StatusNotFound, gin.H{"error": "booking not found", "details": err.Error()})
		return
	}

	// get authenticated user id
	var uid uuid.UUID
	if v, ok := c.Get("user_id"); ok {
		switch t := v.(type) {
		case uuid.UUID:
			uid = t
		case string:
			if parsed, err := uuid.Parse(t); err == nil {
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

	// enforce owner-only visibility
	if !b.UserID.Valid || b.UserID.Bytes != uid {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: only booking owner may view this booking"})
		return
	}

	// resolve seat nos
	pgIDs := make([]pgtype.UUID, 0, len(b.SeatIds))
	seatIDStrs := make([]string, 0, len(b.SeatIds))
	for _, pgid := range b.SeatIds {
		pgIDs = append(pgIDs, pgid)
		if pgid.Valid {
			seatIDStrs = append(seatIDStrs, pgid.String())
		}
	}

	seatNos := []string{}
	if len(pgIDs) > 0 {
		rows, err := h.db.GetSeatNosByIds(ctx, pgIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load seat numbers", "details": err.Error()})
			return
		}
		for _, s := range rows {
			seatNos = append(seatNos, s)
		}
	}

	resp := BookingResponse{
		ID:        b.ID.String(),
		EventID:   b.EventID.String(),
		SeatNos:   seatNos,
		SeatIds:   seatIDStrs,
		SeatsCnt:  b.Seats,
		Status:    b.Status,
		CreatedAt: b.CreatedAt.Time,
		UpdatedAt: b.UpdatedAt.Time,
	}
	c.JSON(http.StatusOK, resp)
}
