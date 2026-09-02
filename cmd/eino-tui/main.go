package main

import (
	"context"
	"os"

	"github.com/mattsp1290/eino-tui/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cli.ProductionDependencies()))
}
