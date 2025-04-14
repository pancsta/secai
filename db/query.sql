-- name: ListPromptsBySessID :one
SELECT *
FROM prompts
WHERE session_id = ?
LIMIT 1;

-- name: AddPrompt :one
INSERT INTO prompts (session_id, agent, state, history_len, system, request, created_at, mach_time_sum, mach_time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: AddPromptResponse :exec
UPDATE prompts
SET response=?
WHERE id = ?
RETURNING id;

-- name: DropPrompts :exec
DROP TABLE prompts;
