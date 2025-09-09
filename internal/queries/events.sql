-- name: GetAllEvents :many
SELECT * FROM events ORDER BY start_time LIMIT $1 OFFSET $2;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = $1;

-- name: AddEvent :one
INSERT INTO events (name, venue, start_time, capacity, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, venue, start_time, capacity, metadata, created_at, updated_at;