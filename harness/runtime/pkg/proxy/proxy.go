package proxy

import (
	"context"
	"io"
	"os"
	"strings"

	internalproxy "github.com/wyvernzora/n8n-acp/harness/runtime/internal/proxy"
)

type Config struct {
	Host          string
	Port          string
	WorkerCommand string
	WorkerArgs    []string
	BridgeCommand string
	ErrorWriter   io.Writer
}

func DefaultConfig() Config {
	return Config{
		Host:          "127.0.0.1",
		Port:          "8080",
		WorkerCommand: "opencode",
		WorkerArgs:    []string{"acp", "--cwd", envDefault("OPENCODE_CWD", "/workspace")},
		BridgeCommand: selfPath(),
		ErrorWriter:   os.Stderr,
	}
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.Host = envDefault("ACP_HOST", cfg.Host)
	cfg.Port = envDefault("ACP_PORT", cfg.Port)
	cfg.WorkerCommand = envDefault("ACP_WORKER_COMMAND", cfg.WorkerCommand)
	cfg.BridgeCommand = envDefault("ACP_PROXY_BRIDGE_COMMAND", cfg.BridgeCommand)
	if raw := strings.TrimSpace(os.Getenv("ACP_WORKER_ARGS")); raw != "" {
		cfg.WorkerArgs = strings.Fields(raw)
	}
	return cfg
}

func Run(ctx context.Context, cfg Config) error {
	return internalproxy.Run(ctx, internalproxy.Config{
		Host:          cfg.Host,
		Port:          cfg.Port,
		WorkerCommand: cfg.WorkerCommand,
		WorkerArgs:    cfg.WorkerArgs,
		BridgeCommand: cfg.BridgeCommand,
		ErrorWriter:   cfg.ErrorWriter,
	})
}

func RunBridge(ctx context.Context, socketPath string, acpID string) error {
	return internalproxy.RunBridge(ctx, socketPath, acpID)
}

func selfPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "acp-proxy"
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
