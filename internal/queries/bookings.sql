-- name: GetBookingByEventAndIdempotency :one
SELECT id, event_id, user_id, seats, seat_ids, status, idempotency_key, created_at, updated_at
FROM bookings
WHERE event_id = $1
    AND idempotency_key = $2;

-- name: GetSeatsForBookingForUpdate :many
SELECT id, seat_no, status, hold_token
FROM seats
WHERE event_id = $1
    AND seat_no = ANY($2::text[])
ORDER BY id
FOR UPDATE;

-- name: InsertBooking :one
INSERT INTO bookings (event_id, user_id, seats, seat_ids, status, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, event_id, user_id, seats, seat_ids, status, idempotency_key, created_at;

-- name: UpdateSeatsToBooked :exec
UPDATE seats
SET STATUS = 'booked',
    booking_id = $1,
    hold_expires_at = NULL,
    hold_token = NULL
WHERE id = ANY($2::uuid[]);

-- name: UpdateEventBookedCount :exec
UPDATE events
SET booked_count = booked_count + $1
WHERE id = $2;

-- name: ConvertSeatHoldToConverted :exec
UPDATE seat_holds
SET status = 'converted'
WHERE hold_token = $1;

-- name: GetSeatHoldForUpdateByToken :one
SELECT id, hold_token, event_id, user_id, expires_at, status
FROM seat_holds
WHERE hold_token = $1
FOR UPDATE;




-- name: GetBookingsByUser :many
SELECT id, event_id, user_id, seats, seat_ids, status, idempotency_key, created_at, updated_at
FROM bookings
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: GetBookingByID :one
SELECT id, event_id, user_id, seats, seat_ids, status, idempotency_key, created_at, updated_at
FROM bookings
WHERE id = $1;

-- name: GetSeatNosByIds :many
SELECT seat_no
FROM seats
WHERE id = ANY($1::uuid[])
ORDER BY seat_no;