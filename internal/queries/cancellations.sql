-- name: GetBookingForUpdate :one
SELECT id, event_id, user_id, seats, seat_ids, status, created_at
FROM bookings
WHERE id = $1
FOR UPDATE;

-- name: UpdateBookingToCancelled :exec
UPDATE bookings
SET status = 'cancelled'
WHERE id = $1 AND status = 'active';

-- name: UpdateSeatsToAvailableByIds :exec
UPDATE seats
SET status = 'available',
    booking_id = NULL,
    hold_token = NULL,
    hold_expires_at = NULL
WHERE id = ANY($1::uuid[]);

-- name: UpdateEventBookedCountByDelta :exec
UPDATE events
SET booked_count = booked_count + $1
WHERE id = $2;