// Command skb remaps the side buttons of the Corsair Darkstar.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/elias-knodel/skb/internal/cli"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, version)

	stop()
	os.Exit(code)
}
