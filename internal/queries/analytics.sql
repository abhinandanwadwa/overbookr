-- name: GetBookingsTotalsBetween :one
SELECT
  COUNT(*)::bigint AS total_bookings,
  COALESCE(SUM(seats), 0)::bigint AS total_seats_booked,
  COALESCE(SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END), 0)::bigint AS total_cancellations,
  COALESCE(SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END), 0)::bigint AS total_active
FROM bookings
WHERE created_at >= $1 AND created_at <= $2;

-- name: GetBookingsPerDayBetween :many
SELECT
  (date_trunc('day', created_at))::timestamptz AS day,
  COUNT(*)::bigint AS bookings_count,
  COALESCE(SUM(seats), 0)::bigint AS seats_booked
FROM bookings
WHERE created_at >= $1 AND created_at <= $2
GROUP BY day
ORDER BY day;

-- name: GetTopEventsBySeatsBetween :many
SELECT
  b.event_id,
  e.name,
  COUNT(*)::bigint AS bookings_count,
  COALESCE(SUM(b.seats), 0)::bigint AS seats_booked,
  e.capacity::int AS capacity,
  e.booked_count::int AS booked_count
FROM bookings b
JOIN events e ON e.id = b.event_id
WHERE b.created_at >= $1 AND b.created_at <= $2
GROUP BY b.event_id, e.name, e.capacity, e.booked_count
ORDER BY seats_booked DESC
LIMIT $3;

-- name: GetBookingsByStatusBetween :many
SELECT status, COUNT(*)::bigint AS cnt
FROM bookings
WHERE created_at >= $1 AND created_at <= $2
GROUP BY status;

-- name: GetEventUtilizationBetween :many
SELECT
  e.id AS event_id,
  e.name,
  e.capacity::int AS capacity,
  e.booked_count::int AS booked_count,
  COALESCE(b.cnt, 0)::bigint AS bookings_seats_in_range
FROM events e
LEFT JOIN (
  SELECT event_id, COALESCE(SUM(seats), 0)::bigint AS cnt
  FROM bookings b
  WHERE b.created_at >= $1 AND b.created_at <= $2
  GROUP BY event_id
) b ON b.event_id = e.id
ORDER BY e.booked_count DESC
LIMIT $3;
