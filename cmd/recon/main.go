package main

import (
	"context"
	"os"

	"recon-framework/internal/app"
)

func main() {
	cli := app.NewCLI(os.Stdout, os.Stderr)
	os.Exit(cli.Run(context.Background(), os.Args[1:]))
}
