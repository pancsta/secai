package db

import (
	"database/sql"
	_ "embed"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

//go:embed schema.sql
var schema string

func Open(dir string) (*sql.DB, error) {
	return sql.Open("sqlite3", "file:"+dir+"/secai.sqlite")
}

func Schema() string {
	return schema
}
