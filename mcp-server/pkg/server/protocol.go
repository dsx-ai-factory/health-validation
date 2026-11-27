// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// receive guards what reaches the SDK's own request handling.
//
//   - tools/call with "arguments": null becomes {}. go-sdk v1.8.0 unmarshals null into a
//     nil map and jsonschema-go v0.4.3 then panics writing a default into it, which ends
//     the process. go-sdk v1.9.0-pre.1 fixes it.
//   - A panic anywhere in tools/call becomes a tool error, not a crash.
//   - resources/* and prompts/* get "method not found", as the capabilities say. The SDK
//     would answer them with empty lists.
func receive(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (result mcp.Result, err error) {
		if strings.HasPrefix(method, "resources/") || strings.HasPrefix(method, "prompts/") {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}
		}

		if method != "tools/call" {
			return next(ctx, method, request)
		}

		if call, ok := request.(*mcp.CallToolRequest); ok && call.Params != nil &&
			bytes.Equal(bytes.TrimSpace(call.Params.Arguments), []byte("null")) {
			call.Params.Arguments = json.RawMessage("{}")
		}

		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("a tool call panicked", "panic", fmt.Sprint(recovered))

				result = &mcp.CallToolResult{
					IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "error executing the tool"}},
				}
				err = nil
			}
		}()

		return next(ctx, method, request)
	}
}

// StdioTransport is the SDK's stdio transport with two changes the specs ask for.
//
//   - A line that is not JSON gets a -32700 parse error, and a JSON line that is not a
//     JSON-RPC message gets -32600; the session goes on. The SDK would end the session.
//   - A response to a request the client has cancelled is not sent. The stdio spec says
//     it MUST NOT be; go-sdk v1.8.0 sends it.
//
// JSON-RPC batches (arrays) and lines over mcp.DefaultMaxLineLength are left to the
// SDK, which ends the session on a bad one.
type StdioTransport struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
	Logger *slog.Logger
}

// Connect starts reading Reader.
func (t *StdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	writer := &lockedWriter{writer: t.Writer}
	pipeReader, pipeWriter := io.Pipe()

	go filterLines(t.Reader, pipeWriter, writer, t.Logger)

	inner, err := (&mcp.IOTransport{
		Reader: &joinedCloser{Reader: pipeReader, closers: []io.Closer{pipeReader, t.Reader}}, Writer: writer,
	}).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting stdio: %w", err)
	}

	return &cancelAware{
		Connection: inner, logger: t.Logger, inFlight: map[jsonrpc.ID]bool{}, cancelled: map[jsonrpc.ID]bool{},
	}, nil
}

// lockedWriter keeps the parse errors filterLines writes from interleaving with the
// SDK's messages. The SDK writes each message with one Write call.
type lockedWriter struct {
	writer io.WriteCloser
	mu     sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.writer.Write(p)
}

func (w *lockedWriter) Close() error {
	return w.writer.Close()
}

type joinedCloser struct {
	io.Reader

	closers []io.Closer
}

func (c *joinedCloser) Close() error {
	var errs []error
	for _, closer := range c.closers {
		errs = append(errs, closer.Close())
	}

	return errors.Join(errs...)
}

var (
	parseError     = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}` + "\n")
	invalidRequest = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Invalid Request"}}` + "\n")
)

// filterLines copies each JSON-RPC line from in to out and answers the others itself.
func filterLines(in io.Reader, out *io.PipeWriter, replies io.Writer, logger *slog.Logger) {
	reader := bufio.NewReaderSize(in, 64<<10)

	for {
		line, readErr := readLine(reader, mcp.DefaultMaxLineLength)
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			reply := lineError(trimmed)
			if reply == nil {
				_, err := out.Write(append(trimmed, '\n'))
				if err != nil {
					return
				}
			} else {
				logger.Warn("refused a line that is not a JSON-RPC message", "bytes", len(trimmed))

				if _, err := replies.Write(reply); err != nil {
					_ = out.CloseWithError(err)
					return
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = nil
			}

			_ = out.CloseWithError(readErr)

			return
		}
	}
}

var errLineTooLong = errors.New("an input line is longer than the limit")

// readLine returns the next line, its newline included, or what is left at the end.
func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte

	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)

		if len(line) > limit {
			return nil, errLineTooLong
		}

		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// lineError is the reply for a line that the SDK would end the session over, or nil.
func lineError(line []byte) []byte {
	if !json.Valid(line) {
		return parseError
	}

	switch line[0] {
	case '[':
		return nil
	case '{':
		if _, err := jsonrpc.DecodeMessage(line); err == nil {
			return nil
		}
	}

	return invalidRequest
}

// cancelAware drops the response to a request the client cancelled while it ran.
type cancelAware struct {
	mcp.Connection

	logger    *slog.Logger
	inFlight  map[jsonrpc.ID]bool
	cancelled map[jsonrpc.ID]bool
	mu        sync.Mutex
}

func (c *cancelAware) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.Connection.Read(ctx)
	if err != nil {
		return nil, err
	}

	request, ok := message.(*jsonrpc.Request)
	if !ok {
		return message, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case request.ID.IsValid():
		c.inFlight[request.ID] = true
	case request.Method == "notifications/cancelled":
		var params struct {
			RequestID any `json:"requestId"`
		}

		if json.Unmarshal(request.Params, &params) == nil {
			// Only a request still running, so the set never outgrows what is in flight.
			if id, err := jsonrpc.MakeID(params.RequestID); err == nil && c.inFlight[id] {
				c.cancelled[id] = true
			}
		}
	}

	return message, nil
}

func (c *cancelAware) Write(ctx context.Context, message jsonrpc.Message) error {
	if response, ok := message.(*jsonrpc.Response); ok {
		c.mu.Lock()
		cancelled := c.cancelled[response.ID]
		delete(c.inFlight, response.ID)
		delete(c.cancelled, response.ID)
		c.mu.Unlock()

		if cancelled {
			c.logger.Debug("dropped the response to a cancelled request")
			return nil
		}
	}

	return c.Connection.Write(ctx, message)
}
