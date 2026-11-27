// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package e2e drives the real server binary over MCP on stdio, as a client would, against
// lab/hvfixture (hv's real verdict server and catalog over synthetic measurements). With
// HV_SERVE_PORT set it runs against a real `health-validation serve` instead, and the
// fixture-only tests skip. `make e2e` and `make e2e-serve` run it; E2E_RESULTS writes the
// results.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	latestProtocol = "2026-07-28"
	callTimeout    = 3 * time.Minute
	fixtureNode    = "hvfixture-node"
	fixtureSecret  = "FIXTURE-SECRET"
	fixtureSource  = "lab/hvfixture: health-validation's real verdict server and catalog, over synthetic measurements"
)

var binary, fixture string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "hv-mcp-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	code := func() int {
		defer func() { _ = os.RemoveAll(directory) }()

		if err := buildAll(directory); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}

		return m.Run()
	}()

	os.Exit(code)
}

// buildAll builds the server and the fixture without VCS stamps, so the results stay the same
// from commit to commit. E2E_BINARY, when set, names a server binary already built.
func buildAll(directory string) error {
	root, err := filepath.Abs("../..")
	if err != nil {
		return err
	}

	binary = os.Getenv("E2E_BINARY")
	if binary == "" {
		binary = filepath.Join(directory, "health-validation-mcp")
		if err := goBuild(root, binary, "./cmd/health-validation-mcp"); err != nil {
			return err
		}
	}

	if realServe() {
		return nil
	}

	fixture = filepath.Join(directory, "hvfixture")

	return goBuild(filepath.Join(root, "lab", "hvfixture"), fixture, ".")
}

func goBuild(directory, output, target string) error {
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", output, target)
	command.Dir = directory
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		return fmt.Errorf("build %s in %s: %w", target, directory, err)
	}

	return nil
}

func realServe() bool { return os.Getenv("HV_SERVE_PORT") != "" }

func needFixture(t *testing.T) {
	t.Helper()

	if realServe() {
		t.Skip("needs lab/hvfixture; HV_SERVE_PORT points at a real serve")
	}
}

// safeBuffer collects a process's stderr while the test reads it.
type safeBuffer struct {
	buffer bytes.Buffer
	mutex  sync.Mutex
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.Write(data)
}

func (b *safeBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.String()
}

// fixtureProcess is a running lab/hvfixture.
type fixtureProcess struct {
	command *exec.Cmd
	events  *safeBuffer
	claim   string
	port    string
}

func startFixture(t *testing.T, scenario string, hold time.Duration) *fixtureProcess {
	t.Helper()

	events := &safeBuffer{}
	//nolint:gosec // The test's own build and arguments.
	command := exec.Command(fixture, "--scenario", scenario, "--hold", hold.String())
	command.Stderr = events

	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())

	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })

	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "fixture stderr: %s", events.String())

	_, port, err := net.SplitHostPort(strings.TrimSpace(strings.TrimPrefix(line, "listening on ")))
	require.NoError(t, err)

	process := &fixtureProcess{command: command, events: events, port: port}

	for _, event := range process.eventsNamed(t, "started") {
		process.claim, _ = event["claim"].(string)
	}

	return process
}

func (f *fixtureProcess) eventsNamed(t *testing.T, name string) []map[string]any {
	t.Helper()

	var found []map[string]any

	for _, line := range strings.Split(f.events.String(), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil && event["msg"] == name {
			found = append(found, event)
		}
	}

	return found
}

func (f *fixtureProcess) waitFor(t *testing.T, name string, within time.Duration) map[string]any {
	t.Helper()

	deadline := time.Now().Add(within)

	for time.Now().Before(deadline) {
		if events := f.eventsNamed(t, name); len(events) > 0 {
			return events[0]
		}

		time.Sleep(50 * time.Millisecond)
	}

	require.FailNow(t, "the fixture did not log "+name, f.events.String())

	return nil
}

// servePort is where the server under test finds serve: a real one, or the fixture.
func servePort(f *fixtureProcess) string {
	if f == nil {
		return os.Getenv("HV_SERVE_PORT")
	}

	return f.port
}

func serverCommand(t *testing.T, port string, environment map[string]string) *exec.Cmd {
	t.Helper()

	settings := map[string]string{"HV_MCP_SERVE_PORT": port}
	if host := os.Getenv("HV_SERVE_HOST"); host != "" {
		settings["HV_MCP_SERVE_HOST"] = host
	}

	for key, value := range environment {
		settings[key] = value
	}

	if image := os.Getenv("E2E_IMAGE"); image != "" {
		// serve listens on loopback, so the container shares the host's network.
		arguments := []string{
			"run", "--rm", "-i", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
			"--pids-limit=64", "--memory=64m", "--network=host",
		}
		for key, value := range settings {
			arguments = append(arguments, "--env", key+"="+value)
		}

		return exec.Command("docker", append(arguments, image)...) //nolint:gosec // The test's own image.
	}

	command := exec.Command(binary) //nolint:gosec // The test's own build.
	for key, value := range settings {
		command.Env = append(command.Env, key+"="+value)
	}

	return command
}

func evaluateOn() map[string]string { return map[string]string{"HV_MCP_ALLOW_EVALUATE": "true"} }

func connect(t *testing.T, command *exec.Cmd) *mcp.ClientSession {
	t.Helper()

	if command.Stderr == nil {
		command.Stderr = os.Stderr
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil)

	session, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: command}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

func call(t *testing.T, session *mcp.ClientSession, tool string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()

	if arguments == nil {
		arguments = map[string]any{}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	require.NoError(t, err, tool)

	return result
}

func message(result *mcp.CallToolResult) string {
	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(*mcp.TextContent); ok {
			return text.Text
		}
	}

	return ""
}

func structured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)

	var out map[string]any

	require.NoError(t, json.Unmarshal(encoded, &out), message(result))

	return out
}

func decision(t *testing.T, verdict map[string]any) map[string]any {
	t.Helper()

	value, ok := verdict["decision"].(map[string]any)
	require.True(t, ok, "verdict: %v", verdict)

	return value
}

func findingIDs(verdict map[string]any) []string {
	var ids []string

	findings, _ := verdict["findings"].([]any)
	for _, entry := range findings {
		if finding, ok := entry.(map[string]any); ok {
			id, _ := finding["check_id"].(string)
			ids = append(ids, id)
		}
	}

	return ids
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}

	return names
}

// leakFree fails when node identity or tenant text reached the client.
func leakFree(t *testing.T, value any) {
	t.Helper()

	encoded, err := json.Marshal(value)
	require.NoError(t, err)

	text := string(encoded)
	assert.NotContains(t, text, fixtureNode, "node_id must be dropped")
	assert.NotContains(t, text, fixtureSecret, "tenant argv must be redacted")
	assert.NotContains(t, text, "train.py", "tenant process names must be redacted")

	if host, err := os.Hostname(); err == nil && len(host) > 3 {
		assert.NotContains(t, strings.ToLower(text), strings.ToLower(host), "the host name must not appear")
	}
}

// record is one saved tool call.
type record struct {
	Result    any            `json:"result"`
	Arguments map[string]any `json:"arguments"`
	Tool      string         `json:"tool"`
	Message   string         `json:"message,omitempty"`
	IsError   bool           `json:"is_error"`
}

// save writes what the client saw to E2E_RESULTS, after a leak check.
func save(t *testing.T, name, claim string, records ...record) {
	t.Helper()

	directory := os.Getenv("E2E_RESULTS")
	if directory == "" {
		return
	}

	source := fixtureSource
	if realServe() {
		source = "a real health-validation serve: real measurements from this host"
	}

	document := map[string]any{
		"source": source, "scenario": name, "calls": records,
		"recorded": time.Now().UTC().Format(time.RFC3339),
	}
	if claim != "" {
		document["scenario_claim"] = claim
	}

	leakFree(t, document)

	if t.Failed() {
		return
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(directory, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(directory, name+".json"), append(encoded, '\n'), 0o600))
}

func recordOf(t *testing.T, tool string, arguments map[string]any, result *mcp.CallToolResult) record {
	t.Helper()

	saved := record{Tool: tool, Arguments: arguments, IsError: result.IsError}
	if result.StructuredContent != nil {
		saved.Result = structured(t, result)
	} else {
		saved.Message = message(result)
	}

	return saved
}

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	return port
}

// What the server must make of each fixture scenario.
var scenarioAdvice = map[string]struct {
	advice  string
	finding string
	entity  string
}{
	"healthy-gb200":        {advice: "READY"},
	"nvlink-down":          {advice: "DRAIN", finding: "nvlink_active", entity: "GPU-22222222-2222-4222-8222-222222222222"},
	"xid79":                {advice: "DRAIN", finding: "gpu_xid_79", entity: "GPU-11111111-1111-4111-8111-111111111111"},
	"busy-source":          {advice: "RETRY_COLLECTION", finding: "collection_source_busy"},
	"discovery-failure":    {advice: "DRAIN", finding: "architecture_discovery"},
	"unknown-architecture": {advice: "READY_LOW_COVERAGE"},
	"overridden-threshold": {advice: "DRAIN", finding: "gpu_average_power_draw"},
}

func TestFixtureScenarios(t *testing.T) {
	needFixture(t)

	for name, want := range scenarioAdvice {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			served := startFixture(t, name, 0)
			session := connect(t, serverCommand(t, servePort(served), evaluateOn()))

			status := call(t, session, "hv_service_status", nil)
			require.False(t, status.IsError, message(status))

			arguments := map[string]any{"detail": "full"}
			result := call(t, session, "hv_get_verdict", arguments)
			require.False(t, result.IsError, message(result))

			verdict := structured(t, result)
			assert.Equal(t, want.advice, decision(t, verdict)["scheduling_advice"], "%v", verdict)
			assert.Equal(t, false, verdict["version_skew"], "the fixture and the server build the same catalog")
			assert.Len(t, served.eventsNamed(t, "evaluate_received"), 1)

			if want.finding == "" {
				assert.Empty(t, findingIDs(verdict))
			} else {
				assert.Contains(t, findingIDs(verdict), want.finding)
			}

			if want.entity != "" {
				encoded, err := json.Marshal(verdict["findings"])
				require.NoError(t, err)
				assert.Contains(t, string(encoded), want.entity, "the failing GPU is named")
			}

			liveness := structured(t, status)
			if name == "busy-source" {
				assert.Equal(t, "unhealthy", liveness["liveness"])
				assert.Len(t, liveness["stuck_calls"], 1)
			} else {
				assert.Equal(t, "ok", liveness["liveness"])
			}

			coverage, _ := verdict["coverage"].(map[string]any)
			switch name {
			case "unknown-architecture":
				assert.Equal(t, true, coverage["low_coverage"])
			case "healthy-gb200":
				assert.Equal(t, false, coverage["low_coverage"])
				assert.Greater(t, coverage["gpu_checks_run"], 0.0)
				assert.Greater(t, coverage["nvlink_checks_run"], 0.0)
				assert.Greater(t, coverage["xid_checks_run"], 0.0)
			}

			save(t, name, served.claim,
				recordOf(t, "hv_service_status", map[string]any{}, status),
				recordOf(t, "hv_get_verdict", arguments, result))
		})
	}
}

// The arguments reach serve as a fixed request body: cadence, xid_from, and nothing else.
func TestTheRequestServeReceives(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 0)
	session := connect(t, serverCommand(t, servePort(served), evaluateOn()))

	arguments := map[string]any{"cadence": "CONTINUOUS", "xid_lookback_minutes": 60, "detail": "summary"}
	before := time.Now().Unix()
	result := call(t, session, "hv_get_verdict", arguments)
	require.False(t, result.IsError, message(result))

	received := served.waitFor(t, "evaluate_received", 5*time.Second)
	assert.Equal(t, true, received["has_cadence"])
	assert.Equal(t, "CONTINUOUS", received["cadence"])
	assert.Equal(t, true, received["has_xid_from"])
	assert.InDelta(t, float64(before-3600), received["xid_from"], 5)
	assert.Equal(t, false, received["has_xid_to"])
	assert.Equal(t, false, received["has_node_id"])
	assert.Equal(t, false, received["has_execution_context"], "serve keeps its default context")

	verdict := structured(t, result)
	coverage, _ := verdict["coverage"].(map[string]any)
	skipped, _ := coverage["skipped_by_reason"].(map[string]any)
	assert.Greater(t, skipped["CADENCE_MISMATCH"], 0.0, "checks outside the window are skipped: %v", coverage)

	save(t, "healthy-gb200-continuous", served.claim, recordOf(t, "hv_get_verdict", arguments, result))
}

func TestEvaluateIsOffByDefault(t *testing.T) {
	var served *fixtureProcess
	if !realServe() {
		served = startFixture(t, "healthy-gb200", 0)
	}

	for _, value := range []string{"", "1", "TRUE", "yes", " true"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			environment := map[string]string{}
			if value != "" {
				environment["HV_MCP_ALLOW_EVALUATE"] = value
			}

			session := connect(t, serverCommand(t, servePort(served), environment))
			assert.NotContains(t, toolNames(t, session), "hv_get_verdict")

			_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "hv_get_verdict", Arguments: map[string]any{}})
			require.Error(t, err, "an unregistered tool is a protocol error")

			status := call(t, session, "hv_service_status", nil)
			assert.Equal(t, false, structured(t, status)["evaluate_enabled"])

			if value == "" {
				save(t, "evaluate-off", "", record{
					Tool: "hv_get_verdict", Arguments: map[string]any{}, IsError: true, Message: err.Error(),
				}, recordOf(t, "hv_service_status", map[string]any{}, status))
			}
		})
	}

	if served != nil {
		assert.Empty(t, served.eventsNamed(t, "evaluate_received"), "nothing reached serve")
	}
}

func TestEvaluateOnAddsTheTool(t *testing.T) {
	session := connect(t, serverCommand(t, closedPort(t), evaluateOn()))

	assert.Equal(t, []string{
		"hv_describe_check", "hv_explain_remediation", "hv_get_verdict", "hv_list_checks", "hv_service_status",
	}, slices.Sorted(slices.Values(toolNames(t, session))))
}

func TestServeStopped(t *testing.T) {
	session := connect(t, serverCommand(t, closedPort(t), evaluateOn()))

	status := call(t, session, "hv_service_status", nil)
	require.False(t, status.IsError, message(status))
	assert.Equal(t, false, structured(t, status)["reachable"])
	assert.Equal(t, "unreachable", structured(t, status)["liveness"])

	result := call(t, session, "hv_get_verdict", map[string]any{})
	require.False(t, result.IsError, "a failed evaluation is a result, not a tool error")

	verdict := structured(t, result)
	assert.Equal(t, "NO_VERDICT", decision(t, verdict)["scheduling_advice"])
	assert.Contains(t, verdict["error"], "service_not_running")

	save(t, "serve-stopped", "", recordOf(t, "hv_service_status", map[string]any{}, status),
		recordOf(t, "hv_get_verdict", map[string]any{}, result))
}

// startHeld starts a held evaluation and returns when the fixture has it.
func startHeld(t *testing.T, served *fixtureProcess, session *mcp.ClientSession) <-chan *mcp.CallToolResult {
	t.Helper()

	results := make(chan *mcp.CallToolResult, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "hv_get_verdict", Arguments: map[string]any{}})
		if err != nil {
			result = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
		}

		results <- result
	}()

	served.waitFor(t, "evaluate_received", 30*time.Second)

	return results
}

func TestServeKilledMidCall(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 2*time.Minute)
	session := connect(t, serverCommand(t, servePort(served), evaluateOn()))
	results := startHeld(t, served, session)

	require.NoError(t, served.command.Process.Kill())

	killed := time.Now()

	select {
	case result := <-results:
		assert.Less(t, time.Since(killed), 10*time.Second)
		require.False(t, result.IsError, message(result))

		verdict := structured(t, result)
		assert.Equal(t, "NO_VERDICT", decision(t, verdict)["scheduling_advice"])
		assert.NotEmpty(t, verdict["error"])
		save(t, "serve-killed-mid-call", served.claim, recordOf(t, "hv_get_verdict", map[string]any{}, result))
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the call did not end when serve died")
	}
}

func TestSecondCallIsRefused(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 2*time.Minute)
	session := connect(t, serverCommand(t, servePort(served), evaluateOn()))
	results := startHeld(t, served, session)

	second := call(t, session, "hv_get_verdict", map[string]any{})
	assert.True(t, second.IsError)
	assert.Contains(t, message(second), "already running")

	status := call(t, session, "hv_service_status", nil)
	assert.Equal(t, true, structured(t, status)["evaluate_running"])

	time.Sleep(500 * time.Millisecond)
	assert.Len(t, served.eventsNamed(t, "evaluate_received"), 1, "the second call never reached serve")

	save(t, "refused-second-call", served.claim, recordOf(t, "hv_get_verdict", map[string]any{}, second),
		recordOf(t, "hv_service_status", map[string]any{}, status))

	require.NoError(t, served.command.Process.Kill())
	<-results
}

// A cancel notification for a running evaluation must reach serve as a cancelled request.
func TestCancelNotificationReachesServe(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 2*time.Minute)
	raw := startRaw(t, serverCommand(t, servePort(served), evaluateOn()))

	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})
	served.waitFor(t, "evaluate_received", 30*time.Second)

	_, err := raw.stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\n"))
	require.NoError(t, err)

	cancelled := served.waitFor(t, "evaluate_cancelled", 10*time.Second)
	assert.Less(t, cancelled["seconds"], 15.0, "serve saw the cancel, not the hold's end")

	// The stdio spec: no reply to a cancelled request. The next message must answer id 2.
	raw.send(t, 2, "tools/list", map[string]any{})
	assert.Contains(t, resultOf(t, raw.reply(t, 2)), "tools", "the cancelled request got no reply")

	require.NoError(t, raw.stdin.Close())

	for late := range raw.replies {
		assert.NotEqual(t, 1.0, late["id"], "no reply for the cancelled request, even late")
	}
}

// "arguments": null used to panic inside the SDK and end the process, an evaluation in
// flight included.
func TestNullArgumentsKeepTheServerUp(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 2*time.Minute)
	command := serverCommand(t, servePort(served), evaluateOn())

	var stderr bytes.Buffer
	command.Stderr = &stderr

	raw := startRaw(t, command)

	raw.send(t, 1, "tools/call", map[string]any{"name": "hv_get_verdict", "arguments": map[string]any{}})
	served.waitFor(t, "evaluate_received", 30*time.Second)

	for id, tool := range []string{"hv_list_checks", "hv_explain_remediation", "hv_describe_check", "hv_service_status", "hv_get_verdict"} {
		raw.send(t, id+2, "tools/call", map[string]any{"name": tool, "arguments": nil})
		assert.NotNil(t, resultOf(t, raw.reply(t, id+2)), tool)
	}

	require.NoError(t, raw.stdin.Close())

	for range raw.replies {
	}

	require.NoError(t, <-raw.exited, "the server exits cleanly, not on a panic")
	assert.NotContains(t, stderr.String(), "panic")
	assert.Equal(t, 2, strings.Count(stderr.String(), `"msg":"evaluate"`),
		"both evaluate attempts are audited: the cancelled one and the refused one")
}

// SIGTERM mid-evaluation: the request to serve is cancelled, which the fixture sees, and the
// server exits. A server that exited and left the request running would pass a check of
// its own exit alone.
func TestSigtermCancelsEvaluate(t *testing.T) {
	needFixture(t)

	if os.Getenv("E2E_IMAGE") != "" {
		t.Skip("signals go to the docker client, not the server")
	}

	served := startFixture(t, "healthy-gb200", 2*time.Minute)

	var stderr safeBuffer

	command := serverCommand(t, servePort(served), evaluateOn())
	command.Stderr = &stderr
	session := connect(t, command)
	results := startHeld(t, served, session)

	require.NoError(t, command.Process.Signal(syscall.SIGTERM))

	cancelled := served.waitFor(t, "evaluate_cancelled", 15*time.Second)
	assert.Less(t, cancelled["seconds"], 30.0)
	assert.Empty(t, served.eventsNamed(t, "evaluate_answered"), "the held evaluation never completed")

	require.NoError(t, waitForExit(command))
	<-results

	assert.Contains(t, stderr.String(), `"outcome":"cancelled"`, "the attempt is audited as cancelled")
}

func waitForExit(command *exec.Cmd) error {
	for range 300 {
		if command.ProcessState != nil || command.Process.Signal(syscall.Signal(0)) != nil {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return errors.New("the server did not exit")
}

// The owner check reads the real socket table: serve's port held by this user passes, and
// the same port with another user named is refused before any request.
func TestOwnerCheck(t *testing.T) {
	needFixture(t)

	served := startFixture(t, "healthy-gb200", 0)
	self := strconv.Itoa(os.Getuid())
	other := strconv.Itoa(os.Getuid() + 1)

	owned := connect(t, serverCommand(t, servePort(served), map[string]string{
		"HV_MCP_ALLOW_EVALUATE": "true", "HV_MCP_SERVE_UID": self,
	}))
	result := call(t, owned, "hv_get_verdict", map[string]any{})
	assert.Equal(t, "READY", decision(t, structured(t, result))["scheduling_advice"])

	refused := connect(t, serverCommand(t, servePort(served), map[string]string{
		"HV_MCP_ALLOW_EVALUATE": "true", "HV_MCP_SERVE_UID": other,
	}))
	result = call(t, refused, "hv_get_verdict", map[string]any{})
	verdict := structured(t, result)
	assert.Equal(t, "NO_VERDICT", decision(t, verdict)["scheduling_advice"])
	assert.Contains(t, verdict["error"], "wrong_owner")
	assert.Len(t, served.eventsNamed(t, "evaluate_received"), 1, "the refused call never reached serve")

	save(t, "refused-wrong-owner", served.claim, recordOf(t, "hv_get_verdict", map[string]any{}, result))
}

// Against a real serve: status, and one evaluation per window, each from a fresh server
// process so the evaluate rate limit does not stand between them.
func TestRealServe(t *testing.T) {
	if !realServe() {
		t.Skip("set HV_SERVE_PORT to run against a real health-validation serve")
	}

	extra := evaluateOn()
	if uid := os.Getenv("HV_SERVE_UID"); uid != "" {
		extra["HV_MCP_SERVE_UID"] = uid
	}

	session := connect(t, serverCommand(t, servePort(nil), extra))
	status := call(t, session, "hv_service_status", nil)
	require.False(t, status.IsError, message(status))
	require.Equal(t, true, structured(t, status)["reachable"], "serve must be running: %v", structured(t, status))
	save(t, "status", "", recordOf(t, "hv_service_status", map[string]any{}, status))

	for _, cadence := range []string{"ALL", "CONTINUOUS", "PROLOG"} {
		t.Run(cadence, func(t *testing.T) {
			session := connect(t, serverCommand(t, servePort(nil), extra))
			arguments := map[string]any{"cadence": cadence, "detail": "full"}
			result := call(t, session, "hv_get_verdict", arguments)
			require.False(t, result.IsError, message(result))

			verdict := structured(t, result)
			assert.NotEqual(t, "NO_VERDICT", decision(t, verdict)["scheduling_advice"], "%v", verdict)
			assert.Equal(t, false, verdict["version_skew"], "serve and this server should build the same catalog")

			save(t, "verdict-"+strings.ToLower(cadence), "", recordOf(t, "hv_get_verdict", arguments, result))
		})
	}
}

// rawServer speaks JSON-RPC to the server directly, one line at a time, so a test sees
// exactly what is on the wire.
type rawServer struct {
	stdin   io.WriteCloser
	replies chan map[string]any
	exited  chan error
}

func startRaw(t *testing.T, command *exec.Cmd) *rawServer {
	t.Helper()

	stdin, err := command.StdinPipe()
	require.NoError(t, err)

	stdout, err := command.StdoutPipe()
	require.NoError(t, err)

	if command.Stderr == nil {
		command.Stderr = os.Stderr
	}

	require.NoError(t, command.Start())

	raw := &rawServer{stdin: stdin, replies: make(chan map[string]any, 32), exited: make(chan error, 1)}

	go func() {
		defer close(raw.replies)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 4<<20)

		for scanner.Scan() {
			var reply map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				reply = map[string]any{"notJSON": scanner.Text()}
			}

			raw.replies <- reply
		}

		raw.exited <- command.Wait()
	}()

	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
	})

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
	case <-time.After(callTimeout):
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

// Each check quotes a requirement of the 2026-07-28 revision: see docs/mcp-standards.md. This is the built binary, on its real stdio.
func TestItSpeaksTheLatestProtocol(t *testing.T) {
	var served *fixtureProcess
	if !realServe() {
		served = startFixture(t, "healthy-gb200", 0)
	}

	var stderr safeBuffer

	command := serverCommand(t, servePort(served), evaluateOn())
	command.Stderr = &stderr
	raw := startRaw(t, command)

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
	content, ok := verdict["structuredContent"].(map[string]any)
	require.True(t, ok, "verdict: %v", verdict)
	advice := content["decision"].(map[string]any)["scheduling_advice"]

	if served != nil {
		assert.Equal(t, "READY", advice)
	} else {
		assert.NotEqual(t, "NO_VERDICT", advice)
	}

	require.NoError(t, raw.stdin.Close())

	select {
	case err := <-raw.exited:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the server must exit when stdin closes")
	}

	logged := stderr.String()
	assert.Contains(t, logged, "started: waiting for an MCP client")
	assert.Contains(t, logged, `"outcome":"ok"`, "the evaluation is audited on stderr")
}
