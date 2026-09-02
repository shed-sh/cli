package main

import (
	"context"
	"os"
	"os/signal"

	"shed/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	app := cli.New(os.Stdout, os.Stderr, version)
	os.Exit(app.Run(ctx, os.Args[1:]))
}
