package main

import (
	"context"
	"os"

	"github.com/alpernae/recon/internal/app"
)

func main() {
	cli := app.NewCLI(os.Stdout, os.Stderr)
	os.Exit(cli.Run(context.Background(), os.Args[1:]))
}
