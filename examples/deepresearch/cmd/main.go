package main

import (
	"context"

	"github.com/joho/godotenv"
	"github.com/pancsta/secai/examples/deepresearch"
)

func main() {
	// load .env
	_ = godotenv.Load()

	println("cmd/main.go")
	a, err := deepresearch.New(context.Background())
	if err != nil {
		panic(err)
	}
	a.Start()

	select {}
}
