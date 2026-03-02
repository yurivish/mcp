package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/yurivish/mcp/internal/proxy"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := proxy.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
