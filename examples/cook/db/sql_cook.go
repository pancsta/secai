package db

import (
	"database/sql"

	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/pancsta/secai/shared"
	"gorm.io/gorm"
)

type Joke struct {
	ID uint `gorm:"primaryKey"`
	// SessionID uint `gorm:"not null"`
	Text string `gorm:"not null"`
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

	err = dbGorm.AutoMigrate(&Joke{}, &Ingredient{})
	if err != nil {
		return nil, "", err
	}

	db, err := dbGorm.DB()
	if err != nil {
		return nil, "", err
	}

	// get schema
	if schema, err = shared.GetSQLiteSchema(db); err != nil {
		return nil, "", err
	}

	return db, schema, nil
}
