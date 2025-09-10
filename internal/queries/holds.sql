-- name: GetSeatsForEventForUpdate :many
SELECT id, seat_no, status
FROM seats
WHERE event_id = $1
    AND seat_no = ANY($2::text[])
ORDER BY id
FOR UPDATE;

-- name: UpdateSeatsToHeld :exec
UPDATE seats
SET status = 'held',
    hold_expires_at = $1,
    hold_token = $2
WHERE id = ANY($3::uuid[]);

-- name: InsertSeatHold :one
INSERT INTO seat_holds (hold_token, event_id, user_id, seat_ids, expires_at, status)
VALUES ($1, $2, $3, $4, $5, 'active')
RETURNING id, hold_token, expires_at;

-- name: GetExpiredSeatHolds :many
SELECT id, hold_token, event_id, seat_ids
FROM seat_holds
WHERE expires_at <= now() AND status = 'active'
ORDER BY created_at;

-- name: UpdateSeatsToAvailableByHold :exec
UPDATE seats
SET status = 'available',
    hold_expires_at = NULL,
    hold_token = NULL,
    updated_at = now()
WHERE hold_token = $1 AND id = ANY($2::uuid[]);

-- name: MarkSeatHoldExpired :exec
UPDATE seat_holds
SET status = 'expired', updated_at = now()
WHERE id = $1;
