package db

import (
	"database/sql"
	"strings"

	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
)

type Character struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Result string `gorm:"not null"`
}

type Joke struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Text string `gorm:"not null"`
}

type Resource struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Key   string `gorm:"not null"`
	Value string `gorm:"not null"`
}

type Ingredient struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Name   string `gorm:"not null"`
	Amount string `gorm:"not null"`
}

func Open(dbFile string) (conn *sql.DB, schema string, err error) {
	file := gormlite.Open(dbFile)
	dbGorm, err := gorm.Open(file, &gorm.Config{})
	if err != nil {
		return nil, "", err
	}

	err = dbGorm.AutoMigrate(&Character{}, &Joke{}, &Resource{}, &Ingredient{})
	if err != nil {
		return nil, "", err
	}

	db, err := dbGorm.DB()
	if err != nil {
		return nil, "", err
	}

	// get schema
	if schema, err = getSQLiteSchema(db); err != nil {
		return nil, "", err
	}

	return db, schema, nil
}

func getSQLiteSchema(db *sql.DB) (string, error) {
	// Query the master table for the 'sql' column
	// We filter out internal sqlite_ tables and empty entries
	query := `
		SELECT sql 
		FROM sqlite_schema 
		WHERE type IN ('table', 'index', 'trigger', 'view') 
		AND name NOT LIKE 'sqlite_%'
		AND sql IS NOT NULL
		ORDER BY name;
	`

	rows, err := db.Query(query)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var sqlStmt string
		if err := rows.Scan(&sqlStmt); err != nil {
			return "", err
		}
		// remove backticks
		sqlStmt = strings.ReplaceAll(sqlStmt, "`", "")
		sb.WriteString(sqlStmt)
		// Append a semicolon and newline for readability
		sb.WriteString(";\n")
	}

	return sb.String(), nil
}
