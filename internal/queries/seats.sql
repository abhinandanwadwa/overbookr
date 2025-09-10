-- name: GetSeatsByEvent :many
SELECT id, seat_no, status, booking_id, created_at, updated_at
FROM seats
WHERE event_id = $1
ORDER BY seat_no;

-- name: BulkInsertSeats :many
-- Insert many seat_no values for an event. Do nothing on conflict (preserve existing seats).
INSERT INTO seats (event_id, seat_no)
SELECT $1, s FROM unnest($2::text[]) AS s
ON CONFLICT (event_id, seat_no) DO NOTHING
RETURNING id, seat_no, status, booking_id, created_at, updated_at;