package main

import (
	"os"

	"github.com/thedavidweng/tg-drive-cli/internal/app"
)

func main() {
	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
