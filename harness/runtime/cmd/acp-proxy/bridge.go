package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func runBridge(_ context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: acp-proxy bridge <socket> <acp-id>")
	}
	conn, err := net.Dial("unix", args[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	client := &bridgeClient{conn: conn, pending: map[string]chan rpcMessage{}}
	var connectionID string
	defer func() {
		if connectionID != "" {
			_, _ = client.request("disconnect", rawObject(map[string]any{"connectionId": connectionID}))
		}
	}()
	go client.readLoop()

	return scanLines(os.Stdin, func(line []byte) {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil || len(msg.ID) == 0 || msg.Method == "" {
			return
		}
		result, err := handleMCP(&connectionID, client, args[1], msg)
		if err != nil {
			writeMCPError(msg.ID, -32000, err.Error())
			return
		}
		if result != nil {
			writeMCPResult(msg.ID, result)
		}
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
		writeMCPError(msg.ID, -32601, "Unknown MCP method: "+msg.Method)
		return nil, nil
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
	_ = scanLines(c.conn, func(line []byte) {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}
		key := idKey(msg.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	})
	c.failPending("ACP MCP bridge closed")
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

func writeMCPResult(id json.RawMessage, result any) {
	data, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Result: rawObject(result)})
	fmt.Println(string(data))
}

func writeMCPError(id json.RawMessage, code int, message string) {
	data, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
	fmt.Println(string(data))
}
