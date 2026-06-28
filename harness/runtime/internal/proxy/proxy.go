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

type forwardedRequest struct {
	client     *clientConn
	originalID json.RawMessage
	method     string
}

type workerProxy struct {
	cfg         Config
	bridge      net.Listener
	bridgePath  string
	workerIn    io.WriteCloser
	workerWrite sync.Mutex
	nextClient  atomic.Int64
	nextWorker  atomic.Int64

	mu            sync.Mutex
	clients       map[string]*clientConn
	pendingWorker map[string]forwardedRequest
	sessionOwners map[string]string
	toolOwners    map[string]string
	mcpOwners     map[string]string
}

type clientConn struct {
	id     string
	conn   net.Conn
	write  sync.Mutex
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[string]pendingRequest
}

func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
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

	child, stdin, stdout, err := startWorker(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start acp worker: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if child.Process != nil {
			_ = child.Process.Kill()
		}
		_ = child.Wait()
	}()

	proxy := &workerProxy{
		cfg:           cfg,
		bridge:        bridgeListener,
		bridgePath:    socketPath,
		workerIn:      stdin,
		clients:       map[string]*clientConn{},
		pendingWorker: map[string]forwardedRequest{},
		sessionOwners: map[string]string{},
		toolOwners:    map[string]string{},
		mcpOwners:     map[string]string{},
	}
	defer proxy.closeClients("acp worker closed")
	go proxy.acceptBridges(ctx)

	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("listen ACP proxy: %w", err)
	}
	defer listener.Close()

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- proxy.readWorker(stdout)
		_ = listener.Close()
	}()

	stopClose := context.AfterFunc(ctx, func() {
		_ = listener.Close()
		_ = bridgeListener.Close()
	})
	defer stopClose()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			select {
			case workerErr := <-workerDone:
				if workerErr == nil {
					return errors.New("acp worker closed")
				}
				return workerErr
			default:
			}
			return fmt.Errorf("accept ACP proxy connection: %w", err)
		}
		go func() {
			if err := proxy.handleClient(ctx, conn); err != nil && !errors.Is(err, net.ErrClosed) {
				_, _ = fmt.Fprintln(cfg.ErrorWriter, err)
			}
		}()
	}
}

func (p *workerProxy) acceptBridges(ctx context.Context) {
	stopClose := context.AfterFunc(ctx, func() {
		_ = p.bridge.Close()
	})
	defer stopClose()
	for {
		conn, err := p.bridge.Accept()
		if err != nil {
			return
		}
		go p.handleBridge(conn)
	}
}

func (p *workerProxy) handleBridge(conn net.Conn) {
	defer conn.Close()
	_ = scanLines(conn, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil || msg.Method == "" {
			return nil
		}
		result, err := p.request(acpMethod(msg.Method), msg.Params)
		response := rpcMessage{ID: msg.ID}
		if err != nil {
			response.Error = &rpcError{Message: err.Error()}
		} else {
			response.Result = result
		}
		data, err := json.Marshal(response)
		if err != nil {
			return fmt.Errorf("marshal bridge response: %w", err)
		}
		return writeLine(conn, data)
	})
}

func (p *workerProxy) request(method string, params json.RawMessage) (json.RawMessage, error) {
	client, err := p.clientForMCP(method, params)
	if err != nil {
		return nil, err
	}
	result, err := client.request(method, params)
	if err != nil {
		return nil, err
	}
	switch method {
	case "mcp/connect":
		if connectionID := resultConnectionID(result); connectionID != "" {
			p.mu.Lock()
			p.mcpOwners[connectionID] = client.id
			p.mu.Unlock()
		}
	case "mcp/disconnect":
		if connectionID := paramsConnectionID(params); connectionID != "" {
			p.mu.Lock()
			delete(p.mcpOwners, connectionID)
			p.mu.Unlock()
		}
	}
	return result, nil
}

func (p *workerProxy) clientForMCP(method string, params json.RawMessage) (*clientConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var ownerID string
	switch method {
	case "mcp/connect":
		acpID := paramsACPID(params)
		if acpID == "" {
			return nil, errors.New("mcp/connect missing acpId")
		}
		ownerID = p.toolOwners[acpID]
	default:
		connectionID := paramsConnectionID(params)
		if connectionID == "" {
			return nil, fmt.Errorf("%s missing connectionId", method)
		}
		ownerID = p.mcpOwners[connectionID]
	}
	if ownerID == "" {
		return nil, errors.New("no n8n client owns mcp request")
	}
	client := p.clients[ownerID]
	if client == nil {
		return nil, errors.New("n8n client for mcp request disconnected")
	}
	return client, nil
}

func (c *clientConn) request(method string, params json.RawMessage) (json.RawMessage, error) {
	id := "proxy:" + c.id + ":" + strconv.FormatInt(c.nextID.Add(1), 10)
	idRaw, _ := json.Marshal(id)
	ch := make(chan rpcMessage, 1)
	timer := time.AfterFunc(requestTimeout, func() {
		c.mu.Lock()
		pending := c.pending[string(idRaw)]
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		if pending.ch != nil {
			pending.ch <- rpcMessage{Error: &rpcError{Message: "acp request timed out: " + method}}
		}
	})

	c.mu.Lock()
	c.pending[string(idRaw)] = pendingRequest{ch: ch, timer: timer}
	c.mu.Unlock()

	msg := rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: params}
	data, err := json.Marshal(msg)
	if err != nil {
		c.removePending(string(idRaw))
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

func (c *clientConn) writeClient(line []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	_, err := c.conn.Write(append(line, '\n'))
	return err
}

func (c *clientConn) deliverResponse(msg rpcMessage) bool {
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

func (c *clientConn) removePending(key string) {
	c.mu.Lock()
	if pending, ok := c.pending[key]; ok {
		pending.timer.Stop()
		delete(c.pending, key)
	}
	c.mu.Unlock()
}

func (c *clientConn) failPending(message string) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]pendingRequest{}
	c.mu.Unlock()
	for _, request := range pending {
		request.timer.Stop()
		request.ch <- rpcMessage{Error: &rpcError{Message: message}}
	}
}

func (p *workerProxy) handleClient(ctx context.Context, conn net.Conn) error {
	client := p.addClient(conn)
	defer p.removeClient(client, "n8n client disconnected")

	stopClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopClose()

	return scanLines(conn, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			if client.deliverResponse(msg) {
				return nil
			}
			return p.writeWorker(line)
		}
		if msg.Method != "" && len(msg.ID) > 0 {
			return p.forwardClientRequest(client, line, msg)
		}
		if msg.Method == "session/new" {
			line = rewriteSessionNew(line, p.bridgePath, p.cfg)
		}
		return p.writeWorker(line)
	})
}

func (p *workerProxy) addClient(conn net.Conn) *clientConn {
	id := "client-" + strconv.FormatInt(p.nextClient.Add(1), 10)
	client := &clientConn{id: id, conn: conn, pending: map[string]pendingRequest{}}
	p.mu.Lock()
	p.clients[id] = client
	p.mu.Unlock()
	return client
}

func (p *workerProxy) removeClient(client *clientConn, message string) {
	p.mu.Lock()
	delete(p.clients, client.id)
	for key, forwarded := range p.pendingWorker {
		if forwarded.client.id == client.id {
			delete(p.pendingWorker, key)
		}
	}
	for sessionID, ownerID := range p.sessionOwners {
		if ownerID == client.id {
			delete(p.sessionOwners, sessionID)
		}
	}
	for toolID, ownerID := range p.toolOwners {
		if ownerID == client.id {
			delete(p.toolOwners, toolID)
		}
	}
	for connectionID, ownerID := range p.mcpOwners {
		if ownerID == client.id {
			delete(p.mcpOwners, connectionID)
		}
	}
	p.mu.Unlock()
	client.failPending(message)
	_ = client.conn.Close()
}

func (p *workerProxy) closeClients(message string) {
	p.mu.Lock()
	clients := make([]*clientConn, 0, len(p.clients))
	for _, client := range p.clients {
		clients = append(clients, client)
	}
	p.mu.Unlock()
	for _, client := range clients {
		p.removeClient(client, message)
	}
}

func (p *workerProxy) forwardClientRequest(client *clientConn, line []byte, msg rpcMessage) error {
	if msg.Method == "session/new" {
		p.recordToolOwners(client, msg.Params)
		line = rewriteSessionNew(line, p.bridgePath, p.cfg)
		if err := json.Unmarshal(line, &msg); err != nil {
			return err
		}
	}

	workerID := "client:" + client.id + ":" + strconv.FormatInt(p.nextWorker.Add(1), 10)
	workerIDRaw, _ := json.Marshal(workerID)
	var root map[string]any
	if err := json.Unmarshal(line, &root); err != nil {
		return err
	}
	root["id"] = workerID
	encoded, err := json.Marshal(root)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.pendingWorker[idKey(workerIDRaw)] = forwardedRequest{
		client:     client,
		originalID: append(json.RawMessage(nil), msg.ID...),
		method:     msg.Method,
	}
	p.mu.Unlock()
	if err := p.writeWorker(encoded); err != nil {
		p.mu.Lock()
		delete(p.pendingWorker, idKey(workerIDRaw))
		p.mu.Unlock()
		return err
	}
	return nil
}

func (p *workerProxy) readWorker(stdout io.Reader) error {
	return scanLines(stdout, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			_, _ = fmt.Fprintf(p.cfg.ErrorWriter, "ignore non-json acp worker output: %s\n", line)
			return nil
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			return p.routeWorkerResponse(line, msg)
		}
		if msg.Method != "" {
			return p.routeWorkerMessage(line, msg)
		}
		p.broadcast(line)
		return nil
	})
}

func (p *workerProxy) routeWorkerResponse(line []byte, msg rpcMessage) error {
	p.mu.Lock()
	forwarded, ok := p.pendingWorker[idKey(msg.ID)]
	if ok {
		delete(p.pendingWorker, idKey(msg.ID))
	}
	p.mu.Unlock()
	if !ok {
		p.broadcast(line)
		return nil
	}
	msg.ID = forwarded.originalID
	if forwarded.method == "session/new" && msg.Error == nil {
		if sessionID := resultSessionID(msg.Result); sessionID != "" {
			p.mu.Lock()
			p.sessionOwners[sessionID] = forwarded.client.id
			p.mu.Unlock()
		}
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return forwarded.client.writeClient(encoded)
}

func (p *workerProxy) routeWorkerMessage(line []byte, msg rpcMessage) error {
	sessionID := paramsSessionID(msg.Params)
	if sessionID == "" {
		if len(msg.ID) > 0 {
			return p.writeWorkerError(msg.ID, -32602, "worker request missing sessionId")
		}
		p.broadcast(line)
		return nil
	}

	p.mu.Lock()
	client := p.clients[p.sessionOwners[sessionID]]
	p.mu.Unlock()
	if client == nil {
		if len(msg.ID) > 0 {
			return p.writeWorkerError(msg.ID, -32602, "no n8n client owns session")
		}
		return nil
	}
	return client.writeClient(line)
}

func (p *workerProxy) writeWorker(line []byte) error {
	p.workerWrite.Lock()
	defer p.workerWrite.Unlock()
	return writeLine(p.workerIn, line)
}

func (p *workerProxy) writeWorkerError(id json.RawMessage, code int, message string) error {
	response := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return p.writeWorker(data)
}

func (p *workerProxy) broadcast(line []byte) {
	p.mu.Lock()
	clients := make([]*clientConn, 0, len(p.clients))
	for _, client := range p.clients {
		clients = append(clients, client)
	}
	p.mu.Unlock()
	for _, client := range clients {
		_ = client.writeClient(line)
	}
}

func (p *workerProxy) recordToolOwners(client *clientConn, params json.RawMessage) {
	ids := paramsACPMCPIDs(params)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, acpID := range ids {
		p.toolOwners[acpID] = client.id
	}
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

func paramsACPMCPIDs(params json.RawMessage) []string {
	var body struct {
		MCPServers []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return nil
	}
	ids := make([]string, 0, len(body.MCPServers))
	for _, server := range body.MCPServers {
		if server.Type == "acp" && server.ID != "" {
			ids = append(ids, server.ID)
		}
	}
	return ids
}

func paramsACPID(params json.RawMessage) string {
	var body struct {
		ACPID string `json:"acpId"`
	}
	_ = json.Unmarshal(params, &body)
	return body.ACPID
}

func paramsConnectionID(params json.RawMessage) string {
	var body struct {
		ConnectionID string `json:"connectionId"`
	}
	_ = json.Unmarshal(params, &body)
	return body.ConnectionID
}

func paramsSessionID(params json.RawMessage) string {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &body)
	return body.SessionID
}

func resultConnectionID(result json.RawMessage) string {
	var body struct {
		ConnectionID string `json:"connectionId"`
	}
	_ = json.Unmarshal(result, &body)
	return body.ConnectionID
}

func resultSessionID(result json.RawMessage) string {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(result, &body)
	return body.SessionID
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
