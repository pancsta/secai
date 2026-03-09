-- name: GetCharacter :one
SELECT *
FROM characters
ORDER BY id DESC
LIMIT 1;

-- name: AddCharacter :one
INSERT INTO characters (result)
VALUES (?)
RETURNING id;

-- name: DeleteAllCharacter :exec
DELETE
FROM characters;

-- name: GetResources :many
SELECT *
FROM resources;

-- name: AddResource :one
INSERT INTO resources (key, value)
VALUES (?, ?)
RETURNING id;

-- name: DeleteAllResources :exec
DELETE
FROM resources;