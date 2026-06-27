package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

func scanLines(r io.Reader, fn func([]byte)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) > 0 {
			fn(append([]byte(nil), line...))
		}
	}
	return scanner.Err()
}

func rawObject(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func idKey(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}
