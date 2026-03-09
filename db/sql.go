package db

import (
	"database/sql"
	"time"

	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/pancsta/secai/shared"
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

// AGENT LLM

type Resource struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Key   string `gorm:"not null"`
	Value string `gorm:"not null"`
}

type Character struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Result string `gorm:"not null"`
}

func Open(dbFile string) (conn *sql.DB, schema string, err error) {
	file := gormlite.Open(dbFile)
	dbGorm, err := gorm.Open(file, &gorm.Config{})
	if err != nil {
		return nil, "", err
	}

	err = dbGorm.AutoMigrate(&Prompt{}, &Character{}, &Resource{})
	if err != nil {
		return nil, "", err
	}

	db, err := dbGorm.DB()
	if err != nil {
		return nil, "", err
	}

	// TODO dump SQL queries on config Debug.Misc

	// get schema
	if schema, err = shared.GetSQLiteSchema(db); err != nil {
		return nil, "", err
	}

	return db, schema, nil
}
