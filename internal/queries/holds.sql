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