// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.29.0

package db

import (
	"database/sql"
)

type Character struct {
	ID        int64         `json:"id"`
	SessionID sql.NullInt64 `json:"session_id"`
	Result    string        `json:"result"`
}

type Ingredient struct {
	ID        int64         `json:"id"`
	SessionID sql.NullInt64 `json:"session_id"`
	Name      string        `json:"name"`
	Amount    string        `json:"amount"`
}

type Joke struct {
	ID        int64         `json:"id"`
	SessionID sql.NullInt64 `json:"session_id"`
	Text      string        `json:"text"`
}

type Resource struct {
	ID        int64         `json:"id"`
	SessionID sql.NullInt64 `json:"session_id"`
	Key       string        `json:"key"`
	Value     string        `json:"value"`
}
