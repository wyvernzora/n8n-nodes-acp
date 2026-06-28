package proxy

import (
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRewriteSessionNewReplacesACPServer(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[{"type":"acp","name":"n8n-tools","id":"tools-1"}]}}`)
	out := rewriteSessionNew(in, "/tmp/bridge.sock", "http://127.0.0.1:9999/mcp/", Config{BridgeCommand: "/bin/acp-proxy"})

	var msg struct {
		Params struct {
			MCPServers []struct {
				Type string `json:"type"`
				Name string `json:"name"`
				URL  string `json:"url"`
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
	if server.Type != "http" || server.Name != "n8n-tools" || server.URL != "http://127.0.0.1:9999/mcp/tools-1" {
		t.Fatalf("server = %#v, want http n8n-tools bridge URL", server)
	}
}

func TestRewriteSessionNewFallsBackToStdioServer(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[{"type":"acp","name":"n8n-tools","id":"tools-1"}]}}`)
	out := rewriteSessionNew(in, "/tmp/bridge.sock", "", Config{BridgeCommand: "/bin/acp-proxy"})

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
	server := msg.Params.MCPServers[0]
	if server.Type != "stdio" || server.Name != "n8n-tools" || server.Command != "/bin/acp-proxy" {
		t.Fatalf("server = %#v, want stdio n8n-tools /bin/acp-proxy", server)
	}
	wantArgs := []string{"bridge", "/tmp/bridge.sock", "tools-1"}
	if !slices.Equal(server.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", server.Args, wantArgs)
	}
}

func TestProxyTracksSessionAndMCPOwners(t *testing.T) {
	proxy := &workerProxy{
		clients:       map[string]*clientConn{},
		pendingWorker: map[string]forwardedRequest{},
		sessionOwners: map[string]string{},
		sessionTools:  map[string][]string{},
		toolOwners:    map[string]string{},
		toolReady:     map[string]chan struct{}{},
		toolMCPConn:   map[string]string{},
		mcpOwners:     map[string]string{},
	}
	server, clientSide := net.Pipe()
	defer server.Close()
	defer clientSide.Close()
	go scanLines(clientSide, func([]byte) error { return nil })
	client := &clientConn{id: "client-1", conn: server}

	proxy.recordToolOwners(client, json.RawMessage(`{"mcpServers":[{"type":"acp","id":"tools-1"},{"type":"stdio","id":"local"}]}`))
	if proxy.toolOwners["tools-1"] != client.id {
		t.Fatalf("tool owner = %q, want %q", proxy.toolOwners["tools-1"], client.id)
	}
	if _, ok := proxy.toolOwners["local"]; ok {
		t.Fatal("stdio server should not be tracked as acp tool owner")
	}

	workerID := json.RawMessage(`"worker-1"`)
	clientID := json.RawMessage(`1`)
	proxy.pendingWorker[idKey(workerID)] = forwardedRequest{
		client:     client,
		originalID: clientID,
		method:     "session/new",
	}
	proxy.routeWorkerResponse([]byte(`{"jsonrpc":"2.0","id":"worker-1","result":{"sessionId":"session-1"}}`), rpcMessage{
		ID:     workerID,
		Result: json.RawMessage(`{"sessionId":"session-1"}`),
	})
	if proxy.sessionOwners["session-1"] != client.id {
		t.Fatalf("session owner = %q, want %q", proxy.sessionOwners["session-1"], client.id)
	}
}

func TestWaitForSessionToolsWaitsUntilReady(t *testing.T) {
	ch := make(chan struct{})
	proxy := &workerProxy{
		sessionTools: map[string][]string{"session-1": []string{"tools-1"}},
		toolReady:    map[string]chan struct{}{"tools-1": ch},
	}
	done := make(chan struct{})
	go func() {
		proxy.waitForSessionTools("session-1")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("waitForSessionTools returned before tool was ready")
	case <-time.After(10 * time.Millisecond):
	}
	proxy.markToolReady("tools-1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForSessionTools did not return after tool was ready")
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
