package main

import (
	"os"

	"github.com/pancsta/secai/db"
)

var schemaFile = "db/schema.sql"

func main() {

	// init DB
	_, schema, err := db.Open("file::memory:")
	if err != nil {
		panic(err)
	}

	err = os.WriteFile(schemaFile, []byte(schema), 0644)
	if err != nil {
		panic(err)
	}
}
