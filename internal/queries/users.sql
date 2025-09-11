-- name: CreateUser :one
INSERT INTO users (name, email, password, role)
VALUES ($1, $2, $3, $4)
RETURNING id, name, email, role, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password, role, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, name, email
FROM users
WHERE id = $1;
