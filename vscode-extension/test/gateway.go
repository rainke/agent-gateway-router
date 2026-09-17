//go:build ignore

// Test fixture: run the real gateway without the CLI's user log/PID files.
package main

import (
	"log"
	"os"

	"agr/config"
	"agr/server"
)

func main() {
	cfg, err := config.Load(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if err := server.New(cfg).Start(); err != nil {
		log.Fatal(err)
	}
}
