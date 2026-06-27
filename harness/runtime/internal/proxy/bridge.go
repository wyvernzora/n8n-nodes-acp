package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

type bridgeClient struct {
	conn    net.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[string]chan rpcMessage
}

type mcpError struct {
	code    int
	message string
}

func (e *mcpError) Error() string {
	return e.message
}

func RunBridge(ctx context.Context, socketPath string, acpID string) error {
	if socketPath == "" || acpID == "" {
		return errors.New("usage: acp-proxy bridge <socket> <acp-id>")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect bridge socket: %w", err)
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()

	client := &bridgeClient{conn: conn, pending: map[string]chan rpcMessage{}}
	var connectionID string
	defer func() {
		if connectionID != "" {
			_, _ = client.request("disconnect", rawObject(map[string]any{"connectionId": connectionID}))
		}
	}()
	go client.readLoop()

	return scanLines(os.Stdin, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil || len(msg.ID) == 0 || msg.Method == "" {
			return nil
		}
		result, err := handleMCP(&connectionID, client, acpID, msg)
		if err != nil {
			code := -32000
			var methodErr *mcpError
			if errors.As(err, &methodErr) {
				code = methodErr.code
			}
			return writeMCPError(os.Stdout, msg.ID, code, err.Error())
		}
		if result != nil {
			return writeMCPResult(os.Stdout, msg.ID, result)
		}
		return nil
	})
}

func handleMCP(connectionID *string, client *bridgeClient, acpID string, msg rpcMessage) (any, error) {
	switch msg.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		if params.ProtocolVersion == "" {
			params.ProtocolVersion = "2024-11-05"
		}
		return map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "n8n-acp-tools", "version": "0.0.0"},
		}, nil
	case "notifications/initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list", "tools/call":
		if *connectionID == "" {
			result, err := client.request("connect", rawObject(map[string]any{"acpId": acpID}))
			if err != nil {
				return nil, err
			}
			var body struct {
				ConnectionID string `json:"connectionId"`
			}
			if err := json.Unmarshal(result, &body); err != nil {
				return nil, err
			}
			*connectionID = body.ConnectionID
		}
		params := msg.Params
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		return client.request("message", rawObject(map[string]any{
			"connectionId": *connectionID,
			"method":       msg.Method,
			"params":       params,
		}))
	default:
		return nil, &mcpError{code: -32601, message: "unknown mcp method: " + msg.Method}
	}
}

func (c *bridgeClient) request(method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	ch := make(chan rpcMessage, 1)
	key := string(idRaw)

	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	data, err := json.Marshal(rpcMessage{ID: idRaw, Method: method, Params: params})
	if err != nil {
		c.removePending(key)
		return nil, err
	}
	c.writeMu.Lock()
	_, err = c.conn.Write(append(data, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(key)
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

func (c *bridgeClient) readLoop() {
	_ = scanLines(c.conn, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		key := idKey(msg.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
		return nil
	})
	c.failPending("acp mcp bridge closed")
}

func (c *bridgeClient) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *bridgeClient) failPending(message string) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]chan rpcMessage{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcMessage{Error: &rpcError{Message: message}}
	}
}

func writeMCPResult(w io.Writer, id json.RawMessage, result any) error {
	data, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Result: rawObject(result)})
	if err != nil {
		return fmt.Errorf("marshal mcp result: %w", err)
	}
	return writeLine(w, data)
}

func writeMCPError(w io.Writer, id json.RawMessage, code int, message string) error {
	data, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
	if err != nil {
		return fmt.Errorf("marshal mcp error: %w", err)
	}
	return writeLine(w, data)
}
