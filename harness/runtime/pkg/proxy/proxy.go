// Package proxy exposes the ACP harness proxy runtime for custom sidecar
// binaries.
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
	MCPHost       string
	MCPPort       string
	WorkerCommand string
	WorkerArgs    []string
	BridgeCommand string
	ErrorWriter   io.Writer
}

// DefaultConfig returns the OpenCode sidecar defaults.
func DefaultConfig() Config {
	return Config{
		Host:          "127.0.0.1",
		Port:          "8080",
		MCPHost:       "127.0.0.1",
		MCPPort:       "0",
		WorkerCommand: "opencode",
		WorkerArgs:    []string{"acp", "--cwd", envDefault("OPENCODE_CWD", "/workspace")},
		BridgeCommand: selfPath(),
		ErrorWriter:   os.Stderr,
	}
}

// ConfigFromEnv returns DefaultConfig with ACP_* environment overrides applied.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.Host = envDefault("ACP_HOST", cfg.Host)
	cfg.Port = envDefault("ACP_PORT", cfg.Port)
	cfg.MCPHost = envDefault("ACP_MCP_HOST", cfg.MCPHost)
	cfg.MCPPort = envDefault("ACP_MCP_PORT", cfg.MCPPort)
	cfg.WorkerCommand = envDefault("ACP_WORKER_COMMAND", cfg.WorkerCommand)
	cfg.BridgeCommand = envDefault("ACP_PROXY_BRIDGE_COMMAND", cfg.BridgeCommand)
	if raw := strings.TrimSpace(os.Getenv("ACP_WORKER_ARGS")); raw != "" {
		cfg.WorkerArgs = strings.Fields(raw)
	}
	return cfg
}

// Run listens for ACP TCP connections and multiplexes them through one stdio
// ACP worker.
func Run(ctx context.Context, cfg Config) error {
	return internalproxy.Run(ctx, internalproxy.Config{
		Host:          cfg.Host,
		Port:          cfg.Port,
		MCPHost:       cfg.MCPHost,
		MCPPort:       cfg.MCPPort,
		WorkerCommand: cfg.WorkerCommand,
		WorkerArgs:    cfg.WorkerArgs,
		BridgeCommand: cfg.BridgeCommand,
		ErrorWriter:   cfg.ErrorWriter,
	})
}

// RunBridge starts the stdio MCP bridge for one ACP tool session.
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
