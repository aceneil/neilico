package main

import (
	"context"
	"fmt"
	"os"

	"umpp/cli/internal/app"
)

func main() {
	application := app.New(os.Stdin, os.Stdout, os.Stderr)
	if err := application.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "umppctl:", err)
		os.Exit(1)
	}
}
