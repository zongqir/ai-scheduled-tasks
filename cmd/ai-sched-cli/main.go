package main

import (
	"log"
	"os"

	"ai-sched-cli/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
