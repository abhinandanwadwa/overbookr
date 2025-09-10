-- name: InsertWaitlist :one
INSERT INTO waitlist (event_id, user_id, requested_seats, position, status)
VALUES (
    $1,
    $2,
    $3,
    (SELECT COALESCE(MAX(position), 0) + 1 FROM waitlist WHERE event_id = $1),
    'waiting'
)
RETURNING id, position, created_at;

-- name: GetWaitingListByEvent :many
SELECT id, event_id, user_id, requested_seats, position, status, created_at
FROM waitlist
WHERE event_id = $1 AND status = 'waiting'
ORDER BY position, created_at;

-- name: UpdateWaitlistStatus :exec
UPDATE waitlist
SET status = $2
WHERE id = $1;

-- name: GetAvailableSeatsForEventForUpdate :many
SELECT id, seat_no
FROM seats
WHERE event_id = $1
    AND status = 'available'
ORDER BY id
LIMIT $2
FOR UPDATE;