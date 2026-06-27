package proxy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRewriteSessionNewReplacesACPServer(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[{"type":"acp","name":"n8n-tools","id":"tools-1"}]}}`)
	out := rewriteSessionNew(in, "/tmp/bridge.sock", Config{BridgeCommand: "/bin/acp-proxy"})

	var msg struct {
		Params struct {
			MCPServers []struct {
				Type    string   `json:"type"`
				Name    string   `json:"name"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		} `json:"params"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatalf("unmarshal rewritten session/new: %v", err)
	}
	if len(msg.Params.MCPServers) != 1 {
		t.Fatalf("mcpServers len = %d, want 1", len(msg.Params.MCPServers))
	}
	server := msg.Params.MCPServers[0]
	if server.Type != "stdio" || server.Name != "n8n-tools" || server.Command != "/bin/acp-proxy" {
		t.Fatalf("server = %#v, want stdio n8n-tools /bin/acp-proxy", server)
	}
	wantArgs := []string{"bridge", "/tmp/bridge.sock", "tools-1"}
	if len(server.Args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", server.Args, wantArgs)
	}
	for i := range wantArgs {
		if server.Args[i] != wantArgs[i] {
			t.Fatalf("args = %#v, want %#v", server.Args, wantArgs)
		}
	}
}

func TestScanLinesReturnsCallbackError(t *testing.T) {
	expected := errors.New("stop")
	err := scanLines(strings.NewReader("one\ntwo\n"), func(line []byte) error {
		if string(line) != "one" {
			t.Fatalf("line = %q, want one", line)
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("scanLines error = %v, want %v", err, expected)
	}
}

func TestHandleMCPUnknownMethod(t *testing.T) {
	_, err := handleMCP(nil, nil, "", rpcMessage{Method: "bogus"})
	var methodErr *mcpError
	if !errors.As(err, &methodErr) {
		t.Fatalf("handleMCP error = %T, want *mcpError", err)
	}
	if methodErr.code != -32601 {
		t.Fatalf("method error code = %d, want -32601", methodErr.code)
	}
}
