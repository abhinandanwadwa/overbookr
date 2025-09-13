-- name: GetAllEvents :many
SELECT *
FROM events
WHERE ($3 = '' OR name ILIKE '%' || $3 || '%' OR venue ILIKE '%' || $3 || '%')
ORDER BY start_time
LIMIT $1 OFFSET $2;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = $1;

-- name: AddEvent :one
INSERT INTO events (name, venue, start_time, capacity, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, venue, start_time, capacity, metadata, created_at, updated_at;

-- name: UpdateEvent :one
UPDATE events
SET
  name = COALESCE($2, name),
  venue = COALESCE($3, venue),
  start_time = COALESCE($4, start_time),
  capacity = COALESCE($5, capacity),
  metadata = COALESCE($6, metadata)
WHERE id = $1
RETURNING id, name, venue, start_time, capacity, booked_count, metadata, created_at, updated_at;

-- name: DeleteEvent :one
DELETE FROM events
WHERE id = $1
RETURNING id;