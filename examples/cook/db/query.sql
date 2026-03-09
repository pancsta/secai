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

-- name: GetIngredient :one
SELECT *
FROM ingredients
ORDER BY id DESC
LIMIT 1;

-- name: AddIngredient :one
INSERT INTO ingredients (name, amount)
VALUES (?, ?)
RETURNING id;

-- name: DeleteAllIngredients :exec
DELETE
FROM ingredients;

-- name: DeleteAllJokes :exec
DELETE
FROM jokes;