-- name: GetCharacter :one
SELECT *
FROM characters
ORDER BY id DESC
LIMIT 1;

-- name: AddCharacter :one
INSERT INTO characters (result)
VALUES (?)
RETURNING id;

-- name: GetJokes :many
SELECT *
FROM jokes
ORDER BY id DESC;

-- name: AddJoke :one
INSERT INTO jokes (text)
VALUES (?)
RETURNING id;

-- name: RemoveJoke :exec
DELETE
FROM jokes
WHERE id = ?;

-- name: GetResources :many
SELECT *
FROM resources;

-- name: AddResource :one
INSERT INTO resources (key, value)
VALUES (?, ?)
RETURNING id;

-- name: GetIngredient :one
SELECT *
FROM ingredients
ORDER BY id DESC
LIMIT 1;

-- name: AddIngredient :one
INSERT INTO ingredients (name, amount)
VALUES (?, ?)
RETURNING id;


-- name: DeleteAllResources :exec
DELETE
FROM resources;

-- name: DeleteAllIngredients :exec
DELETE
FROM ingredients;

-- name: DeleteAllJokes :exec
DELETE
FROM jokes;

-- name: DeleteAllCharacter :exec
DELETE
FROM characters;