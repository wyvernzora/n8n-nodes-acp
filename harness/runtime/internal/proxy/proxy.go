package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const requestTimeout = 10 * time.Minute
const mcpStartupWait = 5 * time.Second

type Config struct {
	Host          string
	Port          string
	MCPHost       string
	MCPPort       string
	WorkerCommand string
	WorkerArgs    []string
	BridgeCommand string
	ErrorWriter   io.Writer
	Logger        *slog.Logger
}

type pendingRequest struct {
	ch    chan rpcMessage
	timer *time.Timer
}

type forwardedRequest struct {
	client     *clientConn
	originalID json.RawMessage
	method     string
	toolIDs    []string
}

type workerProxy struct {
	cfg         Config
	bridge      net.Listener
	bridgePath  string
	mcpHTTP     net.Listener
	mcpHTTPURL  string
	workerIn    io.WriteCloser
	logger      *slog.Logger
	workerWrite sync.Mutex
	nextClient  atomic.Int64
	nextWorker  atomic.Int64

	mu            sync.Mutex
	clients       map[string]*clientConn
	pendingWorker map[string]forwardedRequest
	sessionOwners map[string]string
	sessionTools  map[string][]string
	toolOwners    map[string]string
	toolReady     map[string]chan struct{}
	toolMCPConn   map[string]string
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
	logger := cfg.Logger.With(
		slog.String("component", "acp_proxy"),
		slog.String("acp_addr", net.JoinHostPort(cfg.Host, cfg.Port)),
	)
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

	mcpHTTPListener, err := net.Listen("tcp", net.JoinHostPort(cfg.MCPHost, cfg.MCPPort))
	if err != nil {
		return fmt.Errorf("listen MCP HTTP bridge: %w", err)
	}
	defer mcpHTTPListener.Close()
	mcpHTTPURL := "http://" + mcpHTTPListener.Addr().String() + "/mcp/"
	logger.InfoContext(ctx, "starting acp proxy",
		slog.String("mcp_addr", mcpHTTPListener.Addr().String()),
		slog.String("worker_command", cfg.WorkerCommand),
		slog.Int("worker_args_count", len(cfg.WorkerArgs)),
	)

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
		mcpHTTP:       mcpHTTPListener,
		mcpHTTPURL:    mcpHTTPURL,
		workerIn:      stdin,
		logger:        logger,
		clients:       map[string]*clientConn{},
		pendingWorker: map[string]forwardedRequest{},
		sessionOwners: map[string]string{},
		sessionTools:  map[string][]string{},
		toolOwners:    map[string]string{},
		toolReady:     map[string]chan struct{}{},
		toolMCPConn:   map[string]string{},
		mcpOwners:     map[string]string{},
	}
	defer proxy.closeClients("acp worker closed")
	go proxy.acceptBridges(ctx)
	go proxy.serveMCPHTTP(ctx)

	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("listen ACP proxy: %w", err)
	}
	defer listener.Close()
	logger.InfoContext(ctx, "acp proxy listening")

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
				logger.WarnContext(ctx, "acp client failed", slog.Any("err", err))
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

func (p *workerProxy) log() *slog.Logger {
	if p.logger != nil {
		return p.logger
	}
	return defaultLogger(io.Discard)
}

func (p *workerProxy) serveMCPHTTP(ctx context.Context) {
	server := &http.Server{Handler: http.HandlerFunc(p.handleMCPHTTP)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(p.mcpHTTP); err != nil && !errors.Is(err, http.ErrServerClosed) {
		p.log().WarnContext(ctx, "mcp http bridge stopped with error", slog.Any("err", err))
	}
}

func (p *workerProxy) handleMCPHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	acpID := strings.TrimPrefix(r.URL.Path, "/mcp/")
	if acpID == "" || acpID == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	defer r.Body.Close()

	var msg rpcMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil || msg.Method == "" {
		p.log().WarnContext(r.Context(), "rejected invalid mcp http request",
			slog.String("acp_id", acpID),
			slog.String("remote_addr", r.RemoteAddr),
		)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	result, err := p.handleMCP(acpID, msg)
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	response := rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	if err != nil {
		p.log().WarnContext(r.Context(), "mcp http request failed",
			slog.String("acp_id", acpID),
			slog.String("method", msg.Method),
			slog.Any("err", err),
		)
		code := -32000
		var methodErr *mcpError
		if errors.As(err, &methodErr) {
			code = methodErr.code
		}
		response.Error = &rpcError{Code: code, Message: err.Error()}
	} else {
		switch {
		case result != nil:
			response.Result = rawObject(result)
		default:
			response.Result = json.RawMessage(`{}`)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		p.log().WarnContext(r.Context(), "write mcp http response failed", slog.Any("err", err))
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

func (p *workerProxy) handleMCP(acpID string, msg rpcMessage) (any, error) {
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
		connectionID, err := p.mcpConnection(acpID)
		if err != nil {
			return nil, err
		}
		params := msg.Params
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		result, err := p.request("mcp/message", rawObject(map[string]any{
			"connectionId": connectionID,
			"method":       msg.Method,
			"params":       params,
		}))
		if err == nil && msg.Method == "tools/list" {
			p.markToolReady(acpID)
		}
		return result, err
	default:
		return nil, &mcpError{code: -32601, message: "unknown mcp method: " + msg.Method}
	}
}

func (p *workerProxy) mcpConnection(acpID string) (string, error) {
	p.mu.Lock()
	connectionID := p.toolMCPConn[acpID]
	p.mu.Unlock()
	if connectionID != "" {
		return connectionID, nil
	}

	result, err := p.request("mcp/connect", rawObject(map[string]any{"acpId": acpID}))
	if err != nil {
		return "", err
	}
	connectionID = resultConnectionID(result)
	if connectionID == "" {
		return "", errors.New("mcp/connect did not return connectionId")
	}
	p.mu.Lock()
	p.toolMCPConn[acpID] = connectionID
	p.mu.Unlock()
	p.log().Debug("mcp tool connected",
		slog.String("acp_id", acpID),
		slog.String("connection_id", connectionID),
	)
	return connectionID, nil
}

func (p *workerProxy) markToolReady(acpID string) {
	p.mu.Lock()
	ch := p.toolReady[acpID]
	if ch != nil {
		close(ch)
		delete(p.toolReady, acpID)
	}
	p.mu.Unlock()
}

func (p *workerProxy) waitForSessionTools(sessionID string) {
	if sessionID == "" {
		return
	}
	p.mu.Lock()
	toolIDs := append([]string(nil), p.sessionTools[sessionID]...)
	waiting := make([]chan struct{}, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		if ch := p.toolReady[toolID]; ch != nil {
			waiting = append(waiting, ch)
		}
	}
	p.mu.Unlock()
	if len(waiting) == 0 {
		return
	}

	timer := time.NewTimer(mcpStartupWait)
	defer timer.Stop()
	for _, ch := range waiting {
		select {
		case <-ch:
		case <-timer.C:
			p.log().Warn("timed out waiting for mcp tools",
				slog.String("session_id", sessionID),
				slog.Int("tool_count", len(toolIDs)),
				slog.Duration("timeout", mcpStartupWait),
			)
			return
		}
	}
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
	p.log().InfoContext(ctx, "acp client connected",
		slog.String("client_id", client.id),
		slog.String("remote_addr", conn.RemoteAddr().String()),
	)

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
			line = rewriteSessionNew(line, p.bridgePath, p.mcpHTTPURL, p.cfg)
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
			if toolIDs := p.sessionTools[sessionID]; len(toolIDs) > 0 {
				for _, toolID := range toolIDs {
					delete(p.toolReady, toolID)
					delete(p.toolMCPConn, toolID)
				}
				delete(p.sessionTools, sessionID)
			}
			delete(p.sessionOwners, sessionID)
		}
	}
	for toolID, ownerID := range p.toolOwners {
		if ownerID == client.id {
			delete(p.toolOwners, toolID)
			delete(p.toolReady, toolID)
			delete(p.toolMCPConn, toolID)
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
	p.log().Info("acp client disconnected", slog.String("client_id", client.id))
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
	var toolIDs []string
	if msg.Method == "session/new" {
		toolIDs = p.recordToolOwners(client, msg.Params)
		if len(toolIDs) > 0 {
			p.log().Info("registered acp toolset",
				slog.String("client_id", client.id),
				slog.Int("tool_count", len(toolIDs)),
			)
		}
		line = rewriteSessionNew(line, p.bridgePath, p.mcpHTTPURL, p.cfg)
		if err := json.Unmarshal(line, &msg); err != nil {
			return err
		}
	}
	if msg.Method == "session/prompt" {
		p.waitForSessionTools(paramsSessionID(msg.Params))
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
		toolIDs:    toolIDs,
	}
	p.mu.Unlock()
	if err := p.writeWorker(encoded); err != nil {
		p.mu.Lock()
		delete(p.pendingWorker, idKey(workerIDRaw))
		p.mu.Unlock()
		return err
	}
	p.log().Debug("forwarded acp request",
		slog.String("client_id", client.id),
		slog.String("method", msg.Method),
	)
	return nil
}

func (p *workerProxy) readWorker(stdout io.Reader) error {
	return scanLines(stdout, func(line []byte) error {
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			p.log().Debug("ignored non-json acp worker output", slog.Int("bytes", len(line)))
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
			if len(forwarded.toolIDs) > 0 {
				p.sessionTools[sessionID] = append([]string(nil), forwarded.toolIDs...)
			}
			p.mu.Unlock()
			p.log().Info("registered acp session",
				slog.String("client_id", forwarded.client.id),
				slog.String("session_id", sessionID),
				slog.Int("tool_count", len(forwarded.toolIDs)),
			)
		}
	}
	if msg.Error != nil {
		p.log().Warn("worker request failed",
			slog.String("client_id", forwarded.client.id),
			slog.String("method", forwarded.method),
			slog.Int("code", msg.Error.Code),
			slog.String("err", msg.Error.Message),
		)
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
		p.log().Warn("worker message missing session id", slog.String("method", msg.Method))
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
		p.log().Warn("worker message for unowned session",
			slog.String("method", msg.Method),
			slog.String("session_id", sessionID),
		)
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

func (p *workerProxy) recordToolOwners(client *clientConn, params json.RawMessage) []string {
	ids := paramsACPMCPIDs(params)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, acpID := range ids {
		p.toolOwners[acpID] = client.id
		if _, ok := p.toolReady[acpID]; !ok {
			p.toolReady[acpID] = make(chan struct{})
		}
	}
	return ids
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

func rewriteSessionNew(line []byte, socketPath string, mcpHTTPURL string, cfg Config) []byte {
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
			"type":    "http",
			"name":    name,
			"url":     mcpHTTPURL + id,
			"headers": []any{},
		}
		if mcpHTTPURL == "" {
			servers[i] = map[string]any{
				"type":    "stdio",
				"name":    name,
				"command": cfg.BridgeCommand,
				"args":    []string{"bridge", socketPath, id},
				"env":     []any{},
			}
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
	if c.MCPHost == "" {
		c.MCPHost = "127.0.0.1"
	}
	if c.MCPPort == "" {
		c.MCPPort = "0"
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
	if c.Logger == nil {
		c.Logger = defaultLogger(c.ErrorWriter)
	}
	return c
}

func defaultLogger(w io.Writer) *slog.Logger {
	level := new(slog.LevelVar)
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ACP_LOG_LEVEL"))) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ACP_LOG_FORMAT")), "text") {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
