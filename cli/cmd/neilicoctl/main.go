package main

import (
	"context"
	"fmt"
	"os"

	"neilico/cli/internal/app"
)

func main() {
	application := app.New(os.Stdin, os.Stdout, os.Stderr)
	if err := application.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "neilicoctl:", err)
		os.Exit(1)
	}
}
