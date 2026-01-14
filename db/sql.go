package db

import (
	"database/sql"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
)

// Prompt represents a prompt record in the database.
type Prompt struct {
	// IDs
	ID        uint   `gorm:"primaryKey"`
	SessionID string `gorm:"not null;index:session"`

	// Content
	Agent      string `gorm:"not null"`
	State      string `gorm:"not null"`
	System     string `gorm:"not null"`
	HistoryLen int    `gorm:"not null"`
	Request    string `gorm:"not null"`
	Provider   string `gorm:"not null"`
	Model      string `gorm:"not null"`
	Response   string

	// Time
	CreatedAt   time.Time `gorm:"not null"`
	MachTimeSum int       `gorm:"not null"`
	MachTime    string    `gorm:"not null"`
}

func Open(dbFile string) (conn *sql.DB, schema string, err error) {
	file := gormlite.Open(dbFile)
	dbGorm, err := gorm.Open(file, &gorm.Config{})
	if err != nil {
		return nil, "", err
	}

	err = dbGorm.AutoMigrate(&Prompt{})
	if err != nil {
		return nil, "", err
	}

	db, err := dbGorm.DB()
	if err != nil {
		return nil, "", err
	}

	// TODO dump SQL queries on config Debug.Misc

	// get schema
	if schema, err = getSQLiteSchema(db); err != nil {
		return nil, "", err
	}

	return db, schema, nil
}

// TODO move to shared, format
// "github.com/maxrichie5/go-sqlfmt/sqlfmt"
// config := sqlfmt.NewDefaultConfig()
// formatted := sqlfmt.Format(rawSQL, config)
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
