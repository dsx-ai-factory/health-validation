// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/hvclient"
)

type recorded struct {
	Method string
	Path   string
	Body   string
}

// fakeServe stands in for `health-validation serve`: it answers /verdict and /healthz
// with what a test sets, and records every request it gets.
type fakeServe struct {
	server *httptest.Server
	// Closed when a blocked /verdict request arrives, and when its context ends.
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}

	verdict        []byte
	liveness       string
	requests       []recorded
	verdictStatus  int
	livenessStatus int
	mu             sync.Mutex
	block          bool
}

func newFakeServe(t *testing.T) *fakeServe {
	t.Helper()

	fake := &fakeServe{
		verdictStatus: http.StatusOK, livenessStatus: http.StatusOK, liveness: `{"status":"ok"}`,
		entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}),
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)

	return fake
}

func (f *fakeServe) serve(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)

	f.mu.Lock()
	f.requests = append(f.requests, recorded{Method: request.Method, Path: request.URL.Path, Body: string(body)})
	verdict, verdictStatus, block := f.verdict, f.verdictStatus, f.block
	liveness, livenessStatus := f.liveness, f.livenessStatus
	f.mu.Unlock()

	switch request.URL.Path {
	case "/healthz":
		writer.WriteHeader(livenessStatus)
		_, _ = writer.Write([]byte(liveness))
	case "/verdict":
		if block {
			close(f.entered)
			select {
			case <-request.Context().Done():
				close(f.cancelled)
				return
			case <-f.release:
			}
		}

		if verdictStatus == http.StatusFound {
			http.Redirect(writer, request, "http://example.invalid/", http.StatusFound)
			return
		}

		writer.WriteHeader(verdictStatus)
		_, _ = writer.Write(verdict)
	default:
		http.NotFound(writer, request)
	}
}

func (f *fakeServe) set(verdict []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.verdict = verdict
}

func (f *fakeServe) setBlock() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.block = true
}

func (f *fakeServe) seen() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]recorded(nil), f.requests...)
}

func (f *fakeServe) port(t *testing.T) int {
	t.Helper()

	_, port, err := net.SplitHostPort(f.server.Listener.Addr().String())
	require.NoError(t, err)

	value, err := strconv.Atoi(port)
	require.NoError(t, err)

	return value
}

// syncBuffer collects audit lines written from several goroutines.
type syncBuffer struct {
	buffer bytes.Buffer
	mu     sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.Write(p)
}

// lines returns each audit line as a map.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var out []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(b.buffer.String()), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		out = append(out, entry)
	}

	return out
}

func (b *syncBuffer) outcomes(t *testing.T) []string {
	t.Helper()

	var out []string

	for _, line := range b.lines(t) {
		if line["msg"] == "evaluate" {
			out = append(out, line["outcome"].(string))
		}
	}

	return out
}

type harness struct {
	server  *Server
	session *mcp.ClientSession
	fake    *fakeServe
	audit   *syncBuffer
	stop    context.CancelFunc
}

type setup struct {
	config Config
	// unlimited lifts the evaluation rate limit, for tests that are not about it.
	unlimited bool
	port      int
	ownerUID  *uint32
	procNet   string
}

// evaluateOn allows evaluation with a limit tests do not hit; rate tests set their own.
func evaluateOn() setup {
	return setup{config: Config{AllowEvaluate: true, EvaluateInterval: MinEvaluateInterval}, unlimited: true}
}

func start(t *testing.T, opts setup) *harness {
	t.Helper()

	fake := newFakeServe(t)

	if opts.config.EvaluateInterval == 0 {
		opts.config.EvaluateInterval = DefaultEvaluateInterval
	}

	port := opts.port
	if port == 0 {
		port = fake.port(t)
	}

	client, err := hvclient.New(hvclient.Config{
		Host: "127.0.0.1", Port: port, EvaluateTimeout: hvclient.MinEvaluateTimeout,
		OwnerUID: opts.ownerUID, ProcNet: opts.procNet,
	})
	require.NoError(t, err)

	shutdown, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)

	audit := &syncBuffer{}
	server := New(shutdown, "test", opts.config, client, slog.New(slog.NewJSONHandler(audit, nil)))
	if opts.unlimited {
		server.evaluate = rate.NewLimiter(rate.Inf, 1)
	}

	serverSide, clientSide := mcp.NewInMemoryTransports()

	serverSession, err := server.MCP().Connect(t.Context(), serverSide, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientSide, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return &harness{server: server, session: session, fake: fake, audit: audit, stop: stop}
}

func (h *harness) call(t *testing.T, tool string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()

	if arguments == nil {
		arguments = map[string]any{}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	result, err := h.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	require.NoError(t, err)

	return result
}

// structured decodes a successful result's structured content into out.
func structured[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()

	require.False(t, result.IsError, "the call failed: %s", text(result))

	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)

	var out T
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

// text is everything a result shows a model, as one string.
func text(result *mcp.CallToolResult) string {
	var out strings.Builder

	for _, content := range result.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			out.WriteString(part.Text)
		}
	}

	if result.StructuredContent != nil {
		raw, _ := json.Marshal(result.StructuredContent)
		out.Write(raw)
	}

	return out.String()
}

func (h *harness) verdict(t *testing.T, fixture []byte, arguments map[string]any) VerdictOutput {
	t.Helper()

	h.fake.set(fixture)

	return structured[VerdictOutput](t, h.call(t, "hv_get_verdict", arguments))
}
