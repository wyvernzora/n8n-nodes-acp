package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	if len(os.Args) > 1 && os.Args[1] == "bridge" {
		err = runBridge(ctx, os.Args[2:])
	} else {
		err = runProxy(ctx)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
