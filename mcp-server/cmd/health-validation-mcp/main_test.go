// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const latestProtocol = "2026-07-28"

func TestHelpGoesToStdoutAndEnds(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer

		_, code, done := parseFlags([]string{flag}, &stdout, &stderr)
		assert.True(t, done)
		assert.Zero(t, code)
		assert.Contains(t, stdout.String(), "HV_MCP_ALLOW_EVALUATE")
		assert.Empty(t, stderr.String())
	}
}

func TestVersionGoesToStdoutAndEnds(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		var stdout bytes.Buffer

		_, code, done := parseFlags([]string{flag}, &stdout, io.Discard)
		assert.True(t, done)
		assert.Zero(t, code)
		assert.Regexp(t, `\(health-validation catalog \S+\)`, stdout.String())
	}
}

func TestDebugRunsTheServer(t *testing.T) {
	for _, flag := range []string{"-d", "--debug"} {
		parsed, _, done := parseFlags([]string{flag}, io.Discard, io.Discard)
		assert.False(t, done)
		assert.True(t, parsed.debug)
	}

	parsed, _, done := parseFlags(nil, io.Discard, io.Discard)
	assert.False(t, done)
	assert.False(t, parsed.debug)
}

// With -d each call to serve leaves one debug line, without a body or an address.
func TestDebugLogsEachCallToServe(t *testing.T) {
	for _, debug := range []bool{true, false} {
		raw := startRawWith(t, options{debug: debug},
			map[string]string{"HV_MCP_SERVE_PORT": closedPort(t), "HV_MCP_ALLOW_EVALUATE": "true"})

		raw.send(t, 1, "tools/call", map[string]any{"name": "hv_service_status", "arguments": map[string]any{}})
		raw.reply(t, 1)
		raw.send(t, 2, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})
		raw.reply(t, 2)
		require.NoError(t, raw.stdin.Close())
		require.NoError(t, <-raw.exited)

		var calls []map[string]any

		for line := range strings.SplitSeq(raw.stderr.String(), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "call to serve" {
				calls = append(calls, entry)
			}
		}

		if !debug {
			assert.Empty(t, calls)
			continue
		}

		require.Len(t, calls, 2, raw.stderr.String())
		assert.Equal(t, "DEBUG", calls[0]["level"])
		assert.Equal(t, "/healthz", calls[0]["path"])
		assert.Equal(t, "POST", calls[1]["method"])
		assert.Equal(t, "/verdict", calls[1]["path"])
		assert.Equal(t, "service_not_running", calls[1]["error_kind"])
		assert.NotContains(t, calls[1], "error", "serve's own text stays out")
	}
}

// The debug line never carries a body: serve here answers with a marker in every body.
func TestDebugLinesHoldNoBody(t *testing.T) {
	const marker = "BODYMARKER-must-not-be-logged"

	fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)

		if request.URL.Path == "/healthz" {
			_, _ = writer.Write([]byte(`{"status":"ok","note":"` + marker + `"}`))
			return
		}

		_, _ = writer.Write([]byte(`{"schema_version":"0.7","overall_severity":"PASS","note":"` + marker + `"}`))
	}))
	t.Cleanup(fake.Close)

	_, port, err := net.SplitHostPort(fake.Listener.Addr().String())
	require.NoError(t, err)

	raw := startRawWith(t, options{debug: true}, map[string]string{"HV_MCP_SERVE_PORT": port, "HV_MCP_ALLOW_EVALUATE": "true"})
	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_service_status", "arguments": map[string]any{}})
	raw.reply(t, 1)
	raw.send(t, 2, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})
	raw.reply(t, 2)
	require.NoError(t, raw.stdin.Close())
	require.NoError(t, <-raw.exited)

	documented := []string{"error_kind", "level", "method", "msg", "path", "seconds", "status", "time"}

	var calls int

	for line := range strings.SplitSeq(raw.stderr.String(), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil || entry["msg"] != "call to serve" {
			continue
		}

		calls++

		assert.NotContains(t, line, marker)

		for key := range entry {
			assert.Contains(t, documented, key, "an undocumented key on a debug line")
		}
	}

	assert.Equal(t, 2, calls, raw.stderr.String())
	assert.NotContains(t, raw.stderr.String(), marker, "no line on stderr holds serve's body")
}

func TestABadCommandLineIsRefused(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"serve"}} {
		var stderr bytes.Buffer

		_, code, done := parseFlags(args, io.Discard, &stderr)
		assert.True(t, done)
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "Usage:")
	}
}

func TestABadSettingStopsTheServer(t *testing.T) {
	for name, value := range map[string]string{
		"HV_MCP_SERVE_HOST":                "192.0.2.1",
		"HV_MCP_SERVE_PORT":                "22",
		"HV_MCP_EVALUATE_INTERVAL_SECONDS": "1",
	} {
		reader, writer := io.Pipe()
		err := run(options{}, func(key string) string {
			if key == name {
				return value
			}

			return ""
		}, reader, nopWriteCloser{io.Discard}, io.Discard)
		assert.Error(t, err, name)
		_ = writer.Close()
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// rawServer runs the server in this process on pipes and speaks raw JSON-RPC to it.
type rawServer struct {
	stdin   io.WriteCloser
	replies chan map[string]any
	stderr  *lockedBuffer
	exited  chan error
}

type lockedBuffer struct {
	buffer bytes.Buffer
	mu     sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

func closedPort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	return strconv.Itoa(port)
}

func startRaw(t *testing.T, env map[string]string) *rawServer {
	t.Helper()

	return startRawWith(t, options{}, env)
}

func startRawWith(t *testing.T, opts options, env map[string]string) *rawServer {
	t.Helper()

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	raw := &rawServer{
		stdin: stdinWriter, replies: make(chan map[string]any, 32), stderr: &lockedBuffer{}, exited: make(chan error, 1),
	}

	go func() {
		raw.exited <- run(opts, func(key string) string { return env[key] }, stdinReader, stdoutWriter, raw.stderr)
		_ = stdoutWriter.Close()
	}()

	t.Cleanup(func() { _ = stdinWriter.Close() })

	go func() {
		defer close(raw.replies)

		scanner := bufio.NewScanner(stdoutReader)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)

		for scanner.Scan() {
			var reply map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				reply = map[string]any{"notJSON": scanner.Text()}
			}

			raw.replies <- reply
		}
	}()

	return raw
}

func (r *rawServer) send(t *testing.T, id int, method string, params map[string]any) {
	t.Helper()

	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    latestProtocol,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}

	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	require.NoError(t, err)

	_, err = r.stdin.Write(append(line, '\n'))
	require.NoError(t, err)
}

func (r *rawServer) reply(t *testing.T, id int) map[string]any {
	t.Helper()

	select {
	case reply, open := <-r.replies:
		require.True(t, open, "the server closed stdout")
		require.Nil(t, reply["notJSON"], "stdout must hold only MCP messages")
		require.InDelta(t, id, reply["id"], 0)

		return reply
	case <-time.After(30 * time.Second):
		require.FailNow(t, "no reply", "request %d", id)
		return nil
	}
}

func resultOf(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()

	result, ok := reply["result"].(map[string]any)
	require.True(t, ok, "reply: %v", reply)

	return result
}

func TestItSpeaksTheLatestProtocol(t *testing.T) {
	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t), "HV_MCP_ALLOW_EVALUATE": "true"})

	raw.send(t, 1, "server/discover", map[string]any{})
	discover := resultOf(t, raw.reply(t, 1))
	assert.Equal(t, "complete", discover["resultType"])
	assert.Contains(t, discover["supportedVersions"], latestProtocol)
	meta, ok := discover["_meta"].(map[string]any)
	require.True(t, ok, "discover: %v", discover)
	assert.NotNil(t, meta["io.modelcontextprotocol/serverInfo"])
	assert.Equal(t, map[string]any{"tools": map[string]any{}}, discover["capabilities"])

	raw.send(t, 2, "tools/list", map[string]any{})
	first := resultOf(t, raw.reply(t, 2))
	assert.Equal(t, "complete", first["resultType"])
	assert.Contains(t, first, "ttlMs")
	assert.Contains(t, first, "cacheScope")
	assert.Len(t, first["tools"], 5)

	raw.send(t, 3, "tools/list", map[string]any{})
	assert.Equal(t, first["tools"], resultOf(t, raw.reply(t, 3))["tools"], "the order must be deterministic")

	raw.send(t, 4, "tools/call", map[string]any{"name": "hv_describe_check", "arguments": map[string]any{"check_id": "nope"}})
	assert.Equal(t, true, resultOf(t, raw.reply(t, 4))["isError"], "bad input is a tool execution error")

	raw.send(t, 5, "tools/call", map[string]any{"name": "does_not_exist", "arguments": map[string]any{}})
	unknown, ok := raw.reply(t, 5)["error"].(map[string]any)
	require.True(t, ok, "an unknown tool is a protocol error")
	assert.InDelta(t, -32602, unknown["code"], 0)

	raw.send(t, 6, "tools/call", map[string]any{"name": "hv_list_checks", "arguments": map[string]any{}})
	listing := resultOf(t, raw.reply(t, 6))
	assert.NotNil(t, listing["structuredContent"])
	assert.NotEmpty(t, listing["content"], "structured results also come as text")

	raw.send(t, 7, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})
	verdict := resultOf(t, raw.reply(t, 7))
	structured, ok := verdict["structuredContent"].(map[string]any)
	require.True(t, ok, "verdict: %v", verdict)
	assert.Equal(t, "NO_VERDICT", structured["decision"].(map[string]any)["scheduling_advice"],
		"no serve is running, so there is no verdict")

	require.NoError(t, raw.stdin.Close())

	select {
	case err := <-raw.exited:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the server must exit when stdin closes")
	}

	logged := raw.stderr.String()
	assert.Contains(t, logged, "started: waiting for an MCP client")
	assert.Contains(t, logged, `"outcome":"not_running"`, "the evaluation attempt is audited on stderr")
}

// listeningSockets returns the inodes of TCP and Unix sockets in LISTEN state that this
// process holds.
func listeningSockets(t *testing.T) []string {
	t.Helper()

	held := map[string]bool{}

	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)

	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			held[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}

	var listening []string

	for _, table := range []string{"tcp", "tcp6"} {
		raw, err := os.ReadFile(filepath.Join("/proc/self/net", table))
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(raw), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) > 9 && fields[3] == "0A" && held[fields[9]] {
				listening = append(listening, table+":"+fields[1])
			}
		}
	}

	if raw, err := os.ReadFile("/proc/self/net/unix"); err == nil {
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			// Num RefCount Protocol Flags Type St Inode Path; flag 00010000 is __SO_ACCEPTCON.
			fields := strings.Fields(line)
			if len(fields) > 6 && fields[3] == "00010000" && held[fields[6]] {
				listening = append(listening, "unix:"+fields[6])
			}
		}
	}

	return listening
}

func TestNoListeningSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}

	require.Empty(t, listeningSockets(t), "the test process itself must not listen")

	// The detector must see a listener when there is one, or an empty result proves nothing.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	assert.NotEmpty(t, listeningSockets(t))
	require.NoError(t, probe.Close())
	require.Empty(t, listeningSockets(t))

	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t), "HV_MCP_ALLOW_EVALUATE": "true"})

	raw.send(t, 1, "server/discover", map[string]any{})
	raw.reply(t, 1)

	for id, tool := range []string{"hv_service_status", "hv_get_verdict", "hv_list_checks"} {
		raw.send(t, id+2, "tools/call", map[string]any{"name": tool, "arguments": map[string]any{}})
		raw.reply(t, id+2)
	}

	assert.Empty(t, listeningSockets(t), "the server speaks stdio and opens no port")
}

// blockingServe answers /verdict only when the request is cancelled. It reports when a
// request arrives and when its context ends.
func blockingServe(t *testing.T) (port string, entered, cancelled chan struct{}) {
	t.Helper()

	entered, cancelled = make(chan struct{}), make(chan struct{})
	release := make(chan struct{})

	fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)

		if request.URL.Path != "/verdict" {
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
			return
		}

		close(entered)

		select {
		case <-request.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	t.Cleanup(fake.Close)
	t.Cleanup(func() { close(release) })

	_, port, err := net.SplitHostPort(fake.Listener.Addr().String())
	require.NoError(t, err)

	return port, entered, cancelled
}

// stopsTheEvaluation starts an evaluation that serve never answers, runs stop, and
// checks that serve saw the request cancelled and the server exited.
func stopsTheEvaluation(t *testing.T, stop func(*rawServer)) {
	t.Helper()

	port, entered, cancelled := blockingServe(t)

	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": port, "HV_MCP_ALLOW_EVALUATE": "true"})
	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the evaluation never reached serve")
	}

	stop(raw)

	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the running evaluation must be cancelled")
	}

	select {
	case err := <-raw.exited:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the server must exit")
	}

	assert.Contains(t, raw.stderr.String(), `"outcome":"cancelled"`)
}

// Serve never answers, so only a cancellation ends these evaluations early.
func TestStdinCloseCancelsEvaluate(t *testing.T) {
	stopsTheEvaluation(t, func(raw *rawServer) { require.NoError(t, raw.stdin.Close()) })
}

// On stdio the SDK waits for running handlers when its context ends but does not cancel
// them, so the server's handlers follow their own shutdown context.
func TestSigtermCancelsEvaluate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM")
	}

	stopsTheEvaluation(t, func(*rawServer) {
		// run has registered for SIGTERM, so the signal reaches it, not the test binary's default.
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	})
}

// writeLine sends one raw line, as a client would.
func (r *rawServer) writeLine(t *testing.T, line string) {
	t.Helper()

	_, err := r.stdin.Write([]byte(line + "\n"))
	require.NoError(t, err)
}

// next returns the next message on stdout, whatever its id.
func (r *rawServer) next(t *testing.T) map[string]any {
	t.Helper()

	select {
	case reply, open := <-r.replies:
		require.True(t, open, "the server closed stdout")
		require.Nil(t, reply["notJSON"], "stdout must hold only MCP messages")

		return reply
	case <-time.After(30 * time.Second):
		require.FailNow(t, "no reply")
		return nil
	}
}

func errorCode(t *testing.T, reply map[string]any) float64 {
	t.Helper()

	failure, ok := reply["error"].(map[string]any)
	require.True(t, ok, "reply: %v", reply)

	code, ok := failure["code"].(float64)
	require.True(t, ok, "reply: %v", reply)

	return code
}

// go-sdk v1.8.0 turns "arguments": null into a nil map and jsonschema-go v0.4.3 then
// panics applying a default, which used to end the process.
func TestNullArgumentsDoNotEndTheServer(t *testing.T) {
	port, entered, cancelled := blockingServe(t)
	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": port, "HV_MCP_ALLOW_EVALUATE": "true"})

	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the evaluation never reached serve")
	}

	for id, tool := range []string{"hv_list_checks", "hv_explain_remediation", "hv_describe_check", "hv_service_status"} {
		raw.send(t, id+2, "tools/call", map[string]any{"name": tool, "arguments": nil})
		assert.NotNil(t, resultOf(t, raw.reply(t, id+2)), tool)
	}

	// The running evaluation holds the lock, so this one is refused, but it is answered.
	raw.send(t, 9, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": nil})
	assert.Equal(t, true, resultOf(t, raw.reply(t, 9))["isError"])

	require.NoError(t, raw.stdin.Close())

	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the running evaluation must be cancelled")
	}

	require.NoError(t, <-raw.exited)
	assert.Contains(t, raw.stderr.String(), `"outcome":"cancelled"`, "the first evaluation is audited")
	assert.Contains(t, raw.stderr.String(), `"outcome":"refused_busy"`, "so is the refused one")
	assert.NotContains(t, raw.stderr.String(), "panic")
}

// A line that is not a JSON-RPC message gets an error and the session goes on. The SDK
// alone would end it.
func TestABadLineGetsAnErrorAndTheSessionGoesOn(t *testing.T) {
	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t)})

	for line, code := range map[string]float64{"not json": -32700, "123": -32600, `{"foo":1}`: -32600} {
		raw.writeLine(t, line)
		reply := raw.next(t)
		assert.Nil(t, reply["id"], line)
		assert.InDelta(t, code, errorCode(t, reply), 0, line)
	}

	raw.send(t, 1, "tools/list", map[string]any{})
	assert.Len(t, resultOf(t, raw.reply(t, 1))["tools"], 4)

	require.NoError(t, raw.stdin.Close())
	require.NoError(t, <-raw.exited)
}

// The stdio spec says a cancelled request MUST NOT get a reply. go-sdk v1.8.0 sends one;
// the server's transport drops it.
func TestACancelledRequestGetsNoReply(t *testing.T) {
	port, entered, cancelled := blockingServe(t)
	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": port, "HV_MCP_ALLOW_EVALUATE": "true"})

	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the evaluation never reached serve")
	}

	raw.writeLine(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"test"}}`)

	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the running evaluation must be cancelled")
	}

	// The audit line is written after the handler returns, so wait for it.
	require.Eventually(t, func() bool { return strings.Contains(raw.stderr.String(), `"outcome":"cancelled"`) },
		10*time.Second, 10*time.Millisecond)

	raw.send(t, 2, "tools/list", map[string]any{})
	reply := raw.next(t)
	assert.InDelta(t, 2, reply["id"], 0, "the next message is the tools/list reply, not the cancelled verdict")

	require.NoError(t, raw.stdin.Close())
	require.NoError(t, <-raw.exited)

	for late := range raw.replies {
		assert.NotEqual(t, 1.0, late["id"], "no reply for the cancelled request, even late")
	}
}

// sendPlain sends a request without the 2026-07-28 _meta, as a client of an older revision does.
func (r *rawServer) sendPlain(t *testing.T, id int, method string, params map[string]any) {
	t.Helper()

	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != 0 {
		message["id"] = id
	}

	line, err := json.Marshal(message)
	require.NoError(t, err)
	r.writeLine(t, string(line))
}

// Older revisions are negotiated through initialize. 2026-07-28 has no initialize, so
// asking for it there gets the newest revision that does.
func TestOlderRevisionsThroughInitialize(t *testing.T) {
	for requested, negotiated := range map[string]string{
		"2025-11-25": "2025-11-25", "2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26",
		"2024-11-05": "2024-11-05", latestProtocol: "2025-11-25", "2023-01-01": "2025-11-25",
	} {
		raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t)})

		raw.sendPlain(t, 1, "initialize", map[string]any{
			"protocolVersion": requested, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1"},
		})
		initialized := resultOf(t, raw.reply(t, 1))
		assert.Equal(t, negotiated, initialized["protocolVersion"], requested)
		assert.Equal(t, map[string]any{"tools": map[string]any{}}, initialized["capabilities"], requested)

		raw.sendPlain(t, 0, "notifications/initialized", map[string]any{})
		raw.sendPlain(t, 2, "tools/list", map[string]any{})
		assert.Len(t, resultOf(t, raw.reply(t, 2))["tools"], 4, requested)

		require.NoError(t, raw.stdin.Close())
		require.NoError(t, <-raw.exited)
	}
}

// Methods the capabilities do not list are not found. Before initialize, go-sdk v1.8.0
// answers with code 0, which JSON-RPC does not define; this pins that SDK behaviour.
func TestUndeclaredMethodsAreNotFound(t *testing.T) {
	raw := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t)})

	for id, method := range []string{"resources/list", "resources/templates/list", "prompts/list"} {
		raw.send(t, id+1, method, map[string]any{})
		assert.InDelta(t, -32601, errorCode(t, raw.reply(t, id+1)), 0, method)
	}

	require.NoError(t, raw.stdin.Close())
	require.NoError(t, <-raw.exited)

	fresh := startRaw(t, map[string]string{"HV_MCP_SERVE_PORT": closedPort(t)})
	fresh.sendPlain(t, 1, "tools/list", map[string]any{})
	assert.InDelta(t, 0, errorCode(t, fresh.reply(t, 1)), 0, "no _meta and no initialize")

	require.NoError(t, fresh.stdin.Close())
	require.NoError(t, <-fresh.exited)
}
