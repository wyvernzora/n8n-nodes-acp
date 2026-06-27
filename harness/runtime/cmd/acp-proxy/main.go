package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/wyvernzora/n8n-acp/harness/runtime/pkg/proxy"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "bridge" {
		if len(args) != 3 {
			return fmt.Errorf("usage: acp-proxy bridge <socket> <acp-id>")
		}
		return proxy.RunBridge(ctx, args[1], args[2])
	}

	cfg := proxy.ConfigFromEnv()
	workerArgs := strings.Join(cfg.WorkerArgs, " ")

	fs := flag.NewFlagSet("acp-proxy", flag.ContinueOnError)
	fs.StringVar(&cfg.Host, "host", cfg.Host, "ACP listen host")
	fs.StringVar(&cfg.Port, "port", cfg.Port, "ACP listen port")
	fs.StringVar(&cfg.WorkerCommand, "worker-command", cfg.WorkerCommand, "ACP worker command")
	fs.StringVar(&workerArgs, "worker-args", workerArgs, "ACP worker arguments")
	fs.StringVar(&cfg.BridgeCommand, "bridge-command", cfg.BridgeCommand, "MCP bridge command")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.WorkerArgs = strings.Fields(workerArgs)
	return proxy.Run(ctx, cfg)
}
