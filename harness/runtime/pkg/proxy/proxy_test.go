package proxy

import "testing"

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("ACP_HOST", "0.0.0.0")
	t.Setenv("ACP_PORT", "9090")
	t.Setenv("ACP_WORKER_COMMAND", "codex")
	t.Setenv("ACP_WORKER_ARGS", "acp --cwd /tmp/work")
	t.Setenv("ACP_PROXY_BRIDGE_COMMAND", "/bin/acp-proxy")

	cfg := ConfigFromEnv()
	if cfg.Host != "0.0.0.0" || cfg.Port != "9090" || cfg.WorkerCommand != "codex" {
		t.Fatalf("cfg = %#v", cfg)
	}
	wantArgs := []string{"acp", "--cwd", "/tmp/work"}
	if len(cfg.WorkerArgs) != len(wantArgs) {
		t.Fatalf("WorkerArgs = %#v, want %#v", cfg.WorkerArgs, wantArgs)
	}
	for i := range wantArgs {
		if cfg.WorkerArgs[i] != wantArgs[i] {
			t.Fatalf("WorkerArgs = %#v, want %#v", cfg.WorkerArgs, wantArgs)
		}
	}
	if cfg.BridgeCommand != "/bin/acp-proxy" {
		t.Fatalf("BridgeCommand = %q", cfg.BridgeCommand)
	}
}
