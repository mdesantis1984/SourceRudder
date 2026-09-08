package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Transport interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Name() string
}

type STDIOTransport struct {
	server *Server
	input  io.Reader
	output io.Writer
}

func NewSTDIOTransport(s *Server) *STDIOTransport {
	return &STDIOTransport{server: s, input: os.Stdin, output: os.Stdout}
}

func (t *STDIOTransport) Name() string { return "stdio" }

func (t *STDIOTransport) Start(ctx context.Context) error {
	scanner := bufio.NewScanner(t.input)
	scanner.Buffer(make([]byte, 64*1024), maxRPCRequestBytes)
	encoder := json.NewEncoder(t.output)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		message := bytes.TrimSpace(scanner.Bytes())
		if len(message) == 0 {
			continue
		}
		if err := t.handleMessage(ctx, message, encoder); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdio request: %w", err)
	}
	return nil
}

func (t *STDIOTransport) handleMessage(ctx context.Context, message []byte, encoder *json.Encoder) error {
	isBatch := message[0] == '['
	requests := make([]rpcRequest, 0, 1)
	if isBatch {
		if err := json.Unmarshal(message, &requests); err != nil || len(requests) == 0 {
			return writeStdioParseError(encoder)
		}
	} else {
		var request rpcRequest
		if err := json.Unmarshal(message, &request); err != nil {
			return writeStdioParseError(encoder)
		}
		requests = append(requests, request)
	}

	responses := make([]interface{}, 0, len(requests))
	for _, request := range requests {
		response, isNotification := t.server.handleRPCRequest(ctx, request)
		if !isNotification {
			responses = append(responses, response)
		}
	}
	if len(responses) == 0 {
		return nil
	}
	if !isBatch {
		return encoder.Encode(responses[0])
	}
	return encoder.Encode(responses)
}

func writeStdioParseError(encoder *json.Encoder) error {
	return encoder.Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      nil,
		"error":   map[string]interface{}{"code": -32700, "message": "parse error"},
	})
}

func (t *STDIOTransport) Stop(ctx context.Context) error {
	return nil
}

type HTTPTransport struct {
	addr   string
	server *Server
}

func NewHTTPTransport(addr string, s *Server) *HTTPTransport {
	return &HTTPTransport{addr: addr, server: s}
}

func (t *HTTPTransport) Name() string { return "http" }

func (t *HTTPTransport) Start(ctx context.Context) error {
	return t.server.Start(ctx)
}

func (t *HTTPTransport) Stop(ctx context.Context) error {
	return t.server.Stop(ctx)
}
