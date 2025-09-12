package handlers

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	mail "github.com/abhinandanwadwa/overbookr/internal/api/utils"
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
	EventID   string `json:"event_id" binding:"required,uuid"`
	HoldToken string `json:"hold_token" binding:"required"`
}

type CreateBookingResponse struct {
	ID          string    `json:"id"`
	EventID     string    `json:"event_id"`
	SeatNumbers []string  `json:"seat_numbers"`
	CreatedAt   time.Time `json:"created_at"`
}

type BookingResponse struct {
	ID          string    `json:"id"`
	EventID     string    `json:"event_id"`
	SeatsCnt    int32     `json:"seats_count"`
	SeatNumbers []string  `json:"seat_numbers"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

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

func SimpleValidateHold(ctx context.Context, q *db.Queries, token string, eventID uuid.UUID, userParam pgtype.UUID, userRole string) (int, string, bool) {
	hold, err := q.GetSeatHoldForUpdateByToken(ctx, token)
	if err != nil {
		return http.StatusNotFound, "hold token not found", false
	}

	if hold.Status != "active" {
		return http.StatusConflict, "hold not active", false
	}

	if hold.ExpiresAt.Valid && hold.ExpiresAt.Time.Before(time.Now()) {
		return http.StatusConflict, "hold expired", false
	}

	if hold.EventID.Valid && hold.EventID.Bytes != eventID {
		return http.StatusConflict, "hold belongs to a different event", false
	}

	if hold.UserID.Valid {
		if !userParam.Valid || hold.UserID.Bytes != userParam.Bytes {
			return http.StatusForbidden, "hold token owned by another user", false
		}
	} else {
		if userRole == "admin" {
			return 0, "", true
		}
		return http.StatusForbidden, "hold token not claimable by this user", false
	}

	return 0, "", true
}

func sendConfirmationMail(resp CreateBookingResponse, userId pgtype.UUID, bookingsHandler *BookingsHandler) {
	log.Println("Preparing to send confirmation email for booking ID:", resp.ID)
	mailer := mail.NewMailer(
		"smtp.gmail.com",
		587,
		os.Getenv("GMAIL_USER"),
		os.Getenv("GMAIL_PASS"),
	)

	user, err := bookingsHandler.db.GetUserByID(context.Background(), userId)
	if err != nil {
		log.Println("failed to get user for sending confirmation email:", err)
	}

	event, err := bookingsHandler.db.GetEventByID(context.Background(), pgtype.UUID{Bytes: uuid.MustParse(resp.EventID), Valid: true})
	if err != nil {
		log.Println("failed to get event for sending confirmation email:", err)
	}

	newResp := mail.CreateBookingResponse{
		ID:          resp.ID,
		EventID:     resp.EventID,
		SeatNumbers: resp.SeatNumbers,
		CreatedAt:   resp.CreatedAt,
	}
	mail.SendConfirmationMail(mailer, newResp, event, user.Email, true)
}

func (h *BookingsHandler) CreateBooking(c *gin.Context) {
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

	eid, err := uuid.Parse(req.EventID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event_id", "details": err.Error()})
		return
	}

	ctx := context.Background()
	eventParam := pgtype.UUID{Bytes: eid, Valid: true}
	idempotencyParam := pgtype.Text{String: idempotencyKey, Valid: true}

	existing, err := h.db.GetBookingByEventAndIdempotency(ctx, db.GetBookingByEventAndIdempotencyParams{
		EventID:        eventParam,
		IdempotencyKey: idempotencyParam,
	})
	if err == nil && existing.ID.Bytes != uuid.Nil {
		seatNumbers, serr := h.db.GetSeatNosByIds(ctx, existing.SeatIds)
		if serr != nil {
			c.JSON(http.StatusConflict, gin.H{
				"error":      "booking already exists for this idempotency key",
				"details":    "please use a new idempotency key if you want to create a new booking",
				"booking_id": existing.ID.String(),
			})
			return
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":        "booking already exists for this idempotency key",
			"details":      "please use a new idempotency key if you want to create a new booking",
			"booking_id":   existing.ID.String(),
			"seat_numbers": seatNumbers,
		})
		return
	}

	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pre-check failed", "details": err.Error()})
		return
	}

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

	var currentUserRole string
	if rv, ok := c.Get("user_role"); ok {
		switch r := rv.(type) {
		case string:
			currentUserRole = r
		case []byte:
			currentUserRole = string(r)
		default:
			currentUserRole = "user"
		}
	} else {
		currentUserRole = "user"
	}

	if status, msg, ok := SimpleValidateHold(ctx, h.db, req.HoldToken, eid, userIDParam, currentUserRole); !ok {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	var seatIDs []pgtype.UUID
	rows, err := h.DB.Query(ctx, `SELECT id FROM seats WHERE hold_token = $1 AND event_id = $2 ORDER BY id`, req.HoldToken, eid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get seats from hold", "details": err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var seatID pgtype.UUID
		if err := rows.Scan(&seatID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scan seat ID", "details": err.Error()})
			return
		}
		seatIDs = append(seatIDs, seatID)
	}

	if len(seatIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no seats found for the provided hold token"})
		return
	}

	backoff := initialBackoff
	for attempt := 0; attempt < createBookingMaxRetries; attempt++ {
		tx, err := h.DB.Begin(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction", "details": err.Error()})
			return
		}

		rolledBack := false
		rollbackIfNeeded := func() {
			if !rolledBack {
				_ = tx.Rollback(ctx)
				rolledBack = true
			}
		}

		q := db.New(tx)

		if status, msg, ok := SimpleValidateHold(ctx, q, req.HoldToken, eid, userIDParam, currentUserRole); !ok {
			rollbackIfNeeded()
			c.JSON(status, gin.H{"error": msg})
			return
		}

		seats, err := q.GetSeatsForBookingByIDs(ctx, seatIDs)
		if err != nil {
			rollbackIfNeeded()
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query seats", "details": err.Error()})
			return
		}

		if len(seats) != len(seatIDs) {
			rollbackIfNeeded()
			c.JSON(http.StatusConflict, gin.H{"error": "some seats no longer available"})
			return
		}

		for _, s := range seats {
			if s.Status != "held" {
				rollbackIfNeeded()
				c.JSON(http.StatusConflict, gin.H{
					"error":  "seat is not held",
					"status": s.Status,
				})
				return
			}
			if !s.HoldToken.Valid || s.HoldToken.String != req.HoldToken {
				rollbackIfNeeded()
				c.JSON(http.StatusConflict, gin.H{
					"error": "seat held by different hold token",
				})
				return
			}
		}

		seatsCount := int32(len(seatIDs))
		status := "active"

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

		rowsAffected, err := q.UpdateEventBookedCount(ctx, db.UpdateEventBookedCountParams{BookedCount: seatsCount, ID: eventParam})
		if err != nil {
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
		if rowsAffected == 0 {
			rollbackIfNeeded()
			c.JSON(http.StatusConflict, gin.H{"error": "event capacity exceeded", "details": "not enough capacity to book the requested seats"})
			return
		}

		if err := q.ConvertSeatHoldToConverted(ctx, req.HoldToken); err != nil {
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

		if err := tx.Commit(ctx); err != nil {
			_ = tx.Rollback(ctx)
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "40001" || pgErr.Code == "40P01" {
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit transaction", "details": err.Error()})
			return
		}

		seatNumbers, serr := h.db.GetSeatNosByIds(ctx, bookingRow.SeatIds)
		if serr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get seat numbers", "details": serr.Error()})
			return
		}

		resp := CreateBookingResponse{
			ID:          bookingRow.ID.String(),
			EventID:     bookingRow.EventID.String(),
			SeatNumbers: seatNumbers,
			CreatedAt:   bookingRow.CreatedAt.Time,
		}
		c.JSON(http.StatusCreated, resp)

		// Send mail for the confirmed booking
		log.Println("Sending confirmation email for booking ID:", resp.ID)
		go sendConfirmationMail(resp, userIDParam, h)

		return
	}

	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not complete booking due to concurrent conflicts; please retry"})
}

func (h *BookingsHandler) GetMyBookings(c *gin.Context) {
	ctx := context.Background()

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

	userParam := pgtype.UUID{Bytes: uid, Valid: true}
	bookings, err := h.db.GetBookingsByUser(ctx, userParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch bookings", "details": err.Error()})
		return
	}

	out := make([]BookingResponse, 0, len(bookings))
	for _, b := range bookings {
		seatNumbers, err := h.db.GetSeatNosByIds(ctx, b.SeatIds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get seat numbers", "details": err.Error()})
			return
		}

		out = append(out, BookingResponse{
			ID:          b.ID.String(),
			EventID:     b.EventID.String(),
			SeatsCnt:    b.Seats,
			SeatNumbers: seatNumbers,
			Status:      b.Status,
			CreatedAt:   b.CreatedAt.Time,
			UpdatedAt:   b.UpdatedAt.Time,
		})
	}

	c.JSON(http.StatusOK, out)
}

func (h *BookingsHandler) GetBookingByID(c *gin.Context) {
	ctx := context.Background()
	bookingIDStr := c.Param("id")
	bookingID, err := uuid.Parse(bookingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid booking id", "details": err.Error()})
		return
	}

	b, err := h.db.GetBookingByID(ctx, pgtype.UUID{Bytes: bookingID, Valid: true})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "booking not found", "details": err.Error()})
		return
	}

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

	if !b.UserID.Valid || b.UserID.Bytes != uid {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: only booking owner may view this booking"})
		return
	}

	seatNumbers, err := h.db.GetSeatNosByIds(ctx, b.SeatIds)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get seat numbers", "details": err.Error()})
		return
	}

	resp := BookingResponse{
		ID:          b.ID.String(),
		EventID:     b.EventID.String(),
		SeatsCnt:    b.Seats,
		SeatNumbers: seatNumbers,
		Status:      b.Status,
		CreatedAt:   b.CreatedAt.Time,
		UpdatedAt:   b.UpdatedAt.Time,
	}
	c.JSON(http.StatusOK, resp)
}
