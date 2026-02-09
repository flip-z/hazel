package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/flip-z/hazel/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	code := cli.Run(ctx, os.Args[1:])
	os.Exit(code)
}
