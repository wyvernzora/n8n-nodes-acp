package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const requestTimeout = 10 * time.Minute

type Config struct {
	Host          string
	Port          string
	WorkerCommand string
	WorkerArgs    []string
	BridgeCommand string
	ErrorWriter   io.Writer
}

type pendingRequest struct {
	ch    chan rpcMessage
	timer *time.Timer
}

type acpConn struct {
	client  net.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[string]pendingRequest
}

func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("listen ACP proxy: %w", err)
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept ACP proxy connection: %w", err)
		}
		go func() {
			if err := handleACP(ctx, cfg, conn); err != nil && !errors.Is(err, net.ErrClosed) {
				_, _ = fmt.Fprintln(cfg.ErrorWriter, err)
			}
		}()
	}
}

func handleACP(ctx context.Context, cfg Config, client net.Conn) error {
	defer client.Close()

	dir, err := os.MkdirTemp("", "acp-mcp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "bridge.sock")
	bridgeListener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer bridgeListener.Close()

	acp := &acpConn{client: client, pending: map[string]pendingRequest{}}
	go acp.acceptBridges(ctx, bridgeListener)

	child, stdin, stdout, err := startWorker(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		if child.Process != nil {
			_ = child.Process.Kill()
		}
		_ = child.Wait()
		acp.failPending("ACP connection closed")
	}()

	go func() {
		_ = scanLines(stdout, func(line []byte) {
			_ = acp.writeClient(line)
		})
		_ = client.Close()
	}()

	return scanLines(client, func(line []byte) {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			_, _ = stdin.Write(append(line, '\n'))
			return
		}
		if len(msg.ID) > 0 && msg.Method == "" && acp.deliverResponse(msg) {
			return
		}
		rewritten := rewriteSessionNew(line, socketPath, cfg)
		_, _ = stdin.Write(append(rewritten, '\n'))
	})
}

func startWorker(ctx context.Context, cfg Config) (*exec.Cmd, io.WriteCloser, io.Reader, error) {
	cmd := exec.CommandContext(ctx, cfg.WorkerCommand, cfg.WorkerArgs...)
	cmd.Stderr = cfg.ErrorWriter
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdin, stdout, nil
}

func (c *acpConn) acceptBridges(ctx context.Context, listener net.Listener) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go c.handleBridge(conn)
	}
}

func (c *acpConn) handleBridge(conn net.Conn) {
	defer conn.Close()
	_ = scanLines(conn, func(line []byte) {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil || msg.Method == "" {
			return
		}
		result, err := c.request(acpMethod(msg.Method), msg.Params)
		response := rpcMessage{ID: msg.ID}
		if err != nil {
			response.Error = &rpcError{Message: err.Error()}
		} else {
			response.Result = result
		}
		data, _ := json.Marshal(response)
		_, _ = conn.Write(append(data, '\n'))
	})
}

func (c *acpConn) request(method string, params json.RawMessage) (json.RawMessage, error) {
	id := "proxy:" + strconv.FormatInt(c.nextID.Add(1), 10)
	idRaw, _ := json.Marshal(id)
	ch := make(chan rpcMessage, 1)
	timer := time.AfterFunc(requestTimeout, func() {
		c.mu.Lock()
		pending := c.pending[string(idRaw)]
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		if pending.ch != nil {
			pending.ch <- rpcMessage{Error: &rpcError{Message: "ACP request timed out: " + method}}
		}
	})

	c.mu.Lock()
	c.pending[string(idRaw)] = pendingRequest{ch: ch, timer: timer}
	c.mu.Unlock()

	msg := rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: params}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := c.writeClient(data); err != nil {
		c.removePending(string(idRaw))
		return nil, err
	}

	resp := <-ch
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	if len(resp.Result) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return resp.Result, nil
}

func (c *acpConn) writeClient(line []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.client.Write(append(line, '\n'))
	return err
}

func (c *acpConn) deliverResponse(msg rpcMessage) bool {
	key := idKey(msg.ID)
	c.mu.Lock()
	pending, ok := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if !ok {
		return false
	}
	pending.timer.Stop()
	pending.ch <- msg
	return true
}

func (c *acpConn) removePending(key string) {
	c.mu.Lock()
	if pending, ok := c.pending[key]; ok {
		pending.timer.Stop()
		delete(c.pending, key)
	}
	c.mu.Unlock()
}

func (c *acpConn) failPending(message string) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]pendingRequest{}
	c.mu.Unlock()
	for _, request := range pending {
		request.timer.Stop()
		request.ch <- rpcMessage{Error: &rpcError{Message: message}}
	}
}

func acpMethod(method string) string {
	switch method {
	case "connect":
		return "mcp/connect"
	case "message":
		return "mcp/message"
	case "disconnect":
		return "mcp/disconnect"
	default:
		return method
	}
}

func rewriteSessionNew(line []byte, socketPath string, cfg Config) []byte {
	var root map[string]any
	if err := json.Unmarshal(line, &root); err != nil || root["method"] != "session/new" {
		return line
	}
	params, ok := root["params"].(map[string]any)
	if !ok {
		return line
	}
	servers, ok := params["mcpServers"].([]any)
	if !ok {
		return line
	}
	for i, value := range servers {
		server, ok := value.(map[string]any)
		if !ok || server["type"] != "acp" {
			continue
		}
		name, _ := server["name"].(string)
		if name == "" {
			name = "n8n-tools"
		}
		id, _ := server["id"].(string)
		servers[i] = map[string]any{
			"type":    "stdio",
			"name":    name,
			"command": cfg.BridgeCommand,
			"args":    []string{"bridge", socketPath, id},
			"env":     []any{},
		}
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return line
	}
	return encoded
}

func (c Config) withDefaults() Config {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == "" {
		c.Port = "8080"
	}
	if c.WorkerCommand == "" {
		c.WorkerCommand = "opencode"
	}
	if c.WorkerArgs == nil {
		c.WorkerArgs = []string{"acp", "--cwd", "/workspace"}
	}
	if c.BridgeCommand == "" {
		c.BridgeCommand = "acp-proxy"
	}
	if c.ErrorWriter == nil {
		c.ErrorWriter = os.Stderr
	}
	return c
}
