// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/agentkit/verdictserver"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/proto/verdictjson"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

func startAppAgent(t *testing.T, agent *verdictAgent) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- verdictserver.ServeListener(ctx, listener, agent.evaluate, verdictserver.WithAfterResponse(agent.afterResponse))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("agent did not shut down")
		}
	})
	return listener.Addr().String()
}

func TestServeMatchesOneShotOverHTTPAndGRPC(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cadenceRunApp().Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code == exitError {
		t.Fatalf("one-shot failed: %s", stderr.String())
	}
	agent := cadenceRunApp().newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	address := startAppAgent(t, agent)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/verdict")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status %d: %s (%v)", response.StatusCode, body, err)
	}
	assertVerdictParity(t, stdout.Bytes(), body)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	verdict, err := healthv1.NewVerdictServiceClient(connection).Evaluate(ctx, &healthv1.EvaluateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	body, err = verdictjson.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	assertVerdictParity(t, stdout.Bytes(), body)
}

func assertVerdictParity(t *testing.T, expected, actual []byte) {
	t.Helper()
	decode := func(data []byte) any {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		var normalize func(any)
		normalize = func(current any) {
			switch current := current.(type) {
			case map[string]any:
				for _, key := range []string{"timestamp", "trace_id", "span_id"} {
					delete(current, key)
				}
				if components, ok := current["affected_components"].([]any); ok {
					sort.Slice(components, func(i, j int) bool {
						return components[i].(map[string]any)["category"].(string) < components[j].(map[string]any)["category"].(string)
					})
				}
				for _, value := range current {
					normalize(value)
				}
			case []any:
				for _, value := range current {
					normalize(value)
				}
			}
		}
		normalize(value)
		return value
	}
	if !reflect.DeepEqual(decode(expected), decode(actual)) {
		t.Fatalf("verdict differs from one-shot (excluding timestamps and trace/span IDs)\nwant: %s\ngot: %s", expected, actual)
	}
}

func TestAgentRejectsInvalidOverridesBeforeHardware(t *testing.T) {
	a := New()
	a.host = panicHost{}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID}, nil)
	for _, request := range []*healthv1.EvaluateRequest{
		nil,
		{NodeId: proto.String(" ")},
		{Cadence: healthv1.SchedulingCadence(99).Enum()},
		{ExecutionContext: healthv1.ExecutionContext(99).Enum()},
		{XidFrom: proto.Int64(-1)},
		{XidTo: proto.Int64(0)},
		{XidTo: proto.Int64(-1)},
		{XidFrom: proto.Int64(200), XidTo: proto.Int64(100)},
		{XidFrom: proto.Int64(100), XidTo: proto.Int64(100)},
	} {
		if _, err := agent.evaluate(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Errorf("request %v: %v", request, err)
		}
	}
}

type blockingAgentSource struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (backend *blockingAgentSource) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	backend.calls.Add(1)
	select {
	case backend.started <- struct{}{}:
	default:
	}
	<-backend.release
	return (gpuSpySensor{called: new(bool)}).Measure(ctx, opts...)
}

func TestAgentKeepsTimedOutNativeWorkSerialized(t *testing.T) {
	backend := &blockingAgentSource{started: make(chan struct{}, 2), release: make(chan struct{})}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	a := withAgentSource(cadenceRunApp(), backend)
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{}); done <- err }()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("collection never started")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("first request = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not return")
	}
	queued, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if verdict, err := agent.evaluate(queued, &healthv1.EvaluateRequest{}); err != nil || verdict.GetOverallSeverity() != healthv1.Severity_UNKNOWN {
		t.Fatalf("queued request = %v", err)
	}
	if got := backend.calls.Load(); got != 1 {
		t.Fatalf("overlapping native collection: %d calls", got)
	}
	close(backend.release)
	waitForAgentIdle(t, agent)
	resumed, stopResumed := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopResumed()
	if _, err := agent.evaluate(resumed, &healthv1.EvaluateRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := backend.calls.Load(); got != 2 {
		t.Fatalf("collection did not resume: %d calls", got)
	}
}

func TestAgentNoVerdictIsServerError(t *testing.T) {
	a := cadenceRunApp().WithCollect(gpuSpySensor{called: new(bool)})
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	if _, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("invalid source registration = %v", err)
	}
}

func TestAgentDiscoveryFailureIsSuccessfulCriticalVerdict(t *testing.T) {
	a := New()
	a.host = erroringHost{b: spyBackend{called: new(bool)}}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	verdict, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.GetOverallSeverity() != healthv1.Severity_CRITICAL || len(verdict.GetCheckResults()) != 1 || verdict.GetCheckResults()[0].GetCheckId() != "architecture_discovery" {
		t.Fatalf("discovery failure = %v", verdict)
	}
}

func TestAgentRequestOverridesDoNotChangeDefaults(t *testing.T) {
	agent := cadenceRunApp().newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	request := &healthv1.EvaluateRequest{
		NodeId: proto.String(" other-node "), Cadence: healthv1.SchedulingCadence_CONTINUOUS.Enum(),
		ExecutionContext: healthv1.ExecutionContext_OPERATOR_INFRA.Enum(),
	}
	options, err := agent.request(request)
	if err != nil {
		t.Fatal(err)
	}
	if options.NodeID != "other-node" || options.Cadence == nil || *options.Cadence != health.Continuous || options.ExecutionContext != health.OperatorInfra {
		t.Fatalf("request options = %+v", options)
	}
	options, err = agent.request(&healthv1.EvaluateRequest{})
	if err != nil || options.NodeID != testNodeID || options.Cadence != nil || options.ExecutionContext != health.TenantGuest {
		t.Fatalf("defaults changed: %+v (%v)", options, err)
	}
}

type closeObservedHost struct {
	hostBackend
	closed chan struct{}
}

func (host closeObservedHost) close() error {
	close(host.closed)
	return nil
}

func TestAgentDefersBackendCloseUntilNativeWorkFinishes(t *testing.T) {
	backend := &blockingAgentSource{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(backend.release)
	a := withAgentSource(cadenceRunApp(), backend)
	closed := make(chan struct{})
	a.host = closeObservedHost{hostBackend: a.host, closed: closed}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{}); finished <- err }()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not finish")
	}
	agent.closeWhenIdle()
	select {
	case <-closed:
		t.Fatal("backend closed while native work was in flight")
	default:
	}
	backend.release <- struct{}{}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("backend was not closed after work finished")
	}
}

func TestAgentCollectorFailureOccursAfterCompleteResponse(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	defer close(release)
	cfg := &Config{Metrics: MetricsConfig{Enabled: true, Endpoint: collector.URL, TimeoutSeconds: 1}}
	agent := cadenceRunApp().newVerdictAgent(cfg, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	address := startAppAgent(t, agent)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/verdict")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"overall_severity"`)) {
		t.Fatalf("response: status=%d body=%s err=%v", response.StatusCode, data, err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metrics never published")
	}
}

func TestServeStartupRejectsBadConfigAndFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--address", " "},
		{"--address", "0.0.0.0:0"},
		{"--address", "[::]:0"},
		{"--address", ":0"},
		{"--address", "192.0.2.1:0"},
		{"--address", "localhost:0"},
		{"--cadence", "invalid"},
		{"--cadence", ""},
		{"--node-id", " "},
		{"--node-id", ""},
		{"--execution-context", "invalid"},
		{"--execution-context", ""},
		{"--config", "/missing/health-config.yml"},
		{"unexpected"},
	} {
		a := New().WithProgramName("test")
		a.host = panicHost{}
		var stderr bytes.Buffer
		if got := a.serve(context.Background(), args, &stderr); got != exitError || strings.TrimSpace(stderr.String()) == "" {
			t.Errorf("%v: exit=%d stderr=%q", args, got, stderr.String())
		}
	}
}

func TestServeListenerRequiresExplicitRemoteOptIn(t *testing.T) {
	for _, tc := range []struct {
		address     string
		allowRemote bool
		valid       bool
	}{
		{"127.0.0.1:0", false, true},
		{"127.0.0.2:8080", false, true},
		{"[::1]:8080", false, true},
		{"[::ffff:127.0.0.1]:8080", false, true},
		{"0.0.0.0:8080", false, false},
		{"[::]:8080", false, false},
		{":8080", false, false},
		{"192.0.2.1:8080", false, false},
		{"localhost:8080", false, false},
		{"0.0.0.0:8080", true, true},
		{"[::]:8080", true, true},
		{"192.0.2.1:8080", true, true},
		{"localhost:8080", true, true},
		{"bad-address", true, false},
	} {
		if err := validateListenAddress(tc.address, tc.allowRemote); (err == nil) != tc.valid {
			t.Errorf("address=%q allowRemote=%v: %v", tc.address, tc.allowRemote, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	if code := cadenceRunApp().serve(ctx, []string{"--address", "0.0.0.0:0", "--allow-remote"}, &stderr); code != exitOK {
		t.Fatalf("explicit remote opt-in exit=%d: %s", code, &stderr)
	}
}

func TestServeCleanShutdownAndListenerFailureExitCodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	if code := cadenceRunApp().serve(ctx, []string{"--address", "127.0.0.1:0", "--node-id", testNodeID}, &stderr); code != exitOK {
		t.Fatalf("clean shutdown exit=%d: %s", code, stderr.String())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stderr.Reset()
	if code := cadenceRunApp().serve(context.Background(), []string{"--address", listener.Addr().String(), "--node-id", testNodeID}, &stderr); code != exitError || !strings.Contains(stderr.String(), "listen") {
		t.Fatalf("listener failure exit=%d: %s", code, stderr.String())
	}
}

func TestServeValidatesOverrideWirePrecisionBeforeListening(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, tc := range []struct {
		name      string
		checkID   string
		parameter string
		value     string
		valid     bool
	}{
		{"ordinary integer", "test_custom_check", "max_count", "8", true},
		{"exact integer boundary", "test_custom_check", "max_count", "9007199254740992", true},
		{"lossy integer", "test_custom_check", "max_count", "9007199254740993", false},
		{"exact integer above boundary", "test_custom_check", "max_count", "9007199254740994", true},
		{"lossy maximum integer", "test_custom_check", "max_count", "9223372036854775807", false},
		{"large floating tolerance", "gpu_average_power_draw", "max_power_w", "1.0e100", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			contents := "checks:\n  " + tc.checkID + ":\n    " + tc.parameter + ": " + tc.value + "\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if tc.valid {
				address = "127.0.0.1:0"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := cadenceRunApp().WithChecks(customCheck())
			a.openJournal = func() (io.WriteCloser, error) {
				cancel()
				return &datagramRecorder{}, nil
			}
			var stderr bytes.Buffer
			code := a.serve(ctx, []string{"--address", address, "--config", path}, &stderr)
			if tc.valid {
				if code != exitOK {
					t.Fatalf("valid override exit=%d: %s", code, &stderr)
				}
			} else if code != exitError || !strings.Contains(stderr.String(), tc.checkID) || !strings.Contains(stderr.String(), tc.parameter) || !strings.Contains(stderr.String(), "loses precision") {
				t.Fatalf("want precision error before binding the occupied address; exit=%d: %s", code, &stderr)
			}
		})
	}
}

type stalledDiscoveryHost struct {
	hostBackend
	release <-chan struct{}
}

func (host stalledDiscoveryHost) gpuInventory() *gpuinventory.Inventory {
	return gpuinventory.New(func() (gpuinventory.Reader, error) {
		<-host.release
		return inventoryReader{}, nil
	})
}

func TestAgentSourceTimeoutProducesVerdict(t *testing.T) {
	for _, tc := range []struct {
		setup   func(chan struct{}) *App
		name    string
		checkID string
	}{
		{func(release chan struct{}) *App {
			return withAgentSource(cadenceRunApp(), &blockingAgentSource{started: make(chan struct{}, 1), release: release})
		}, "GPU source", "gpu_average_power_draw"},
		{func(release chan struct{}) *App {
			a := cadenceRunApp()
			a.host = stalledDiscoveryHost{hostBackend: a.host, release: release}
			return a
		}, "discovery", "architecture_discovery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			agent := tc.setup(release).newVerdictAgent(&Config{CollectionTimeoutSeconds: 1}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			verdict, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{})
			if err != nil {
				t.Fatalf("source watchdog returned a transport error instead of a verdict: %v", err)
			}
			for _, check := range verdict.GetCheckResults() {
				if check.GetCheckId() == tc.checkID {
					if severity := check.GetSeverity(); severity != healthv1.Severity_CRITICAL && severity != healthv1.Severity_UNKNOWN {
						t.Fatalf("collection failure severity = %v", severity)
					}
					return
				}
			}
			t.Fatalf("no failure verdict for %s: %v", tc.checkID, verdict)
		})
	}
}

type delayedAgentGPU struct{}

func (delayedAgentGPU) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	time.Sleep(700 * time.Millisecond)
	return (gpuSpySensor{called: new(bool)}).Measure(ctx, opts...)
}

type delayedAgentNode struct{}

func (delayedAgentNode) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	time.Sleep(700 * time.Millisecond)
	return agentNodeCountSource(4).Measure(ctx, opts...)
}

func TestAgentSequentialSourcesHaveIndependentBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agent := withAgentSource(withAgentSource(cadenceRunApp(), delayedAgentGPU{}), delayedAgentNode{}).newVerdictAgent(&Config{CollectionTimeoutSeconds: 1}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		verdict, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{})
		if err != nil || verdict.GetOverallSeverity() != healthv1.Severity_PASS {
			t.Fatalf("healthy sequential sources: %v, %v", verdict, err)
		}
	})
}

func TestServeValidatesRequestBudgetAgainstSourceTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("collection_timeout_seconds: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []string{"0s", "-1s", "1s", "3s"} {
		t.Run(timeout, func(t *testing.T) {
			a := cadenceRunApp()
			valid := timeout == "3s"
			if !valid {
				a.host = panicHost{}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var stderr bytes.Buffer
			code := a.serve(ctx, []string{"--address", "127.0.0.1:0", "--config", path, "--request-timeout", timeout}, &stderr)
			if valid {
				if code != exitOK {
					t.Fatalf("valid request budget exit=%d: %s", code, &stderr)
				}
			} else if code != exitError || !strings.Contains(stderr.String(), "--request-timeout must exceed collection_timeout_seconds") {
				t.Fatalf("invalid request budget exit=%d: %s", code, &stderr)
			}
		})
	}
}

func TestAgentRequestBudgetIncludesAdmission(t *testing.T) {
	a := New()
	a.host = panicHost{}
	agent := a.newVerdictAgent(&Config{CollectionTimeoutSeconds: 1}, evaluationRequest{NodeID: testNodeID}, nil)
	agent.requestTimeout = 1100 * time.Millisecond
	agent.gate <- struct{}{}
	defer func() { <-agent.gate }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{})
	if status.Code(err) != codes.DeadlineExceeded || time.Since(started) >= 3*time.Second {
		t.Fatalf("queued request exceeded its service budget: %v, elapsed %s", err, time.Since(started))
	}
}

func waitForAgentIdle(t *testing.T, agent *verdictAgent) {
	t.Helper()
	select {
	case agent.gate <- struct{}{}:
		<-agent.gate
	case <-time.After(5 * time.Second):
		t.Fatal("admission did not reopen after the source returned")
	}
}

func TestAgentReportsStuckSourcesToQueuedAndLaterCallers(t *testing.T) {
	exported := make(chan []byte, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		exported <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer collector.Close()
	backend := &blockingAgentSource{started: make(chan struct{}, 2), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(backend.release) })
	cfg := &Config{CollectionTimeoutSeconds: 1, Metrics: MetricsConfig{Enabled: true, Endpoint: collector.URL, TimeoutSeconds: 1}}
	agent := withAgentSource(cadenceRunApp(), backend).newVerdictAgent(cfg, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	firstDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{}); firstDone <- err }()
	select {
	case <-backend.started:
	case <-ctx.Done():
		t.Fatal("source did not start")
	}
	address := startAppAgent(t, agent)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/verdict")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("queued request status=%d body=%s err=%v", response.StatusCode, body, err)
	}
	var busy health.NodeVerdict
	if decodeErr := json.Unmarshal(body, &busy); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if busy.NodeID != testNodeID || busy.OverallSeverity != health.Unknown || len(busy.CheckResults) != 1 || busy.CheckResults[0].CheckID != "collection_source_busy" {
		t.Fatalf("stuck-source verdict = %+v", busy)
	}
	if action := busy.RecommendedAction; len(action.RemediationActions) != 1 || action.RemediationActions[0] != health.RemediationActionRetryCollection {
		t.Fatalf("stuck-source action = %+v", action)
	}
	values := busy.CheckResults[0].ObservedValues
	since, ok := values["since"].(string)
	if values["source"] != "test.blocking.gpu" || !ok {
		t.Fatalf("source metadata = %v", values)
	}
	if _, parseErr := time.Parse(time.RFC3339Nano, since); parseErr != nil {
		t.Fatal(parseErr)
	}
	select {
	case payload := <-exported:
		if !bytes.Contains(payload, []byte("collection_source_busy")) {
			t.Fatal("metrics omitted the stuck-source check")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stuck-source verdict was not published to metrics")
	}
	select {
	case firstErr := <-firstDone:
		if firstErr != nil {
			t.Fatalf("first watchdog returned a transport error: %v", firstErr)
		}
	case <-ctx.Done():
		t.Fatal("first request did not finish after its watchdog")
	}
	later, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
	if err != nil || later.GetOverallSeverity() != healthv1.Severity_UNKNOWN || later.GetCheckResults()[0].GetObservedValues()["since"].GetStringValue() != since || later.GetTraceId() == busy.TraceID {
		t.Fatalf("later request did not produce a fresh verdict for the same unfinished call: %v, %v", later, err)
	}
	if backend.calls.Load() != 1 {
		t.Fatal("a later request overlapped the unfinished call")
	}
	release.Do(func() { close(backend.release) })
	waitForAgentIdle(t, agent)
	resumed, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
	if err != nil || backend.calls.Load() != 2 {
		t.Fatalf("fresh collection did not resume: %v, %v", resumed, err)
	}
	for _, check := range resumed.GetCheckResults() {
		if check.GetCheckId() == "collection_source_busy" {
			t.Fatal("stuck-source verdict remained after recovery")
		}
	}
}

func (*blockingAgentSource) ID() string               { return "test.blocking.gpu" }
func (*blockingAgentSource) Signals() collect.Signals { return agentPowerSignals() }
func (delayedAgentGPU) ID() string                    { return "test.delayed.gpu" }
func (delayedAgentGPU) Signals() collect.Signals      { return agentPowerSignals() }
func (delayedAgentNode) ID() string                   { return "test.delayed.node" }
func (delayedAgentNode) Signals() collect.Signals     { return collect.Signals{gpuinventory.GPUCount} }

func TestAgentXidWindowsOverHTTPAndGRPC(t *testing.T) {
	reader := journal.New(func(_ context.Context, _ []string, out, _ io.Writer) error {
		for _, seconds := range []int64{100, 150, 200, 250, 300} {
			fmt.Fprintf(out, `{"__CURSOR":"c%d","__REALTIME_TIMESTAMP":"%d","_BOOT_ID":"0123456789abcdef0123456789abcdef","MESSAGE":"NVRM: Xid (PCI:0008:06:00): 79"}`+"\n", seconds, seconds*1000000)
		}
		return nil
	})
	source, err := xidsensor.NewWithTimeRanges(reader, proto.Int64(100), proto.Int64(300), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := testApp().newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, source)
	address := startAppAgent(t, agent)
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, transport := range []string{"GET", "POST", "gRPC"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range []struct {
				request *healthv1.EvaluateRequest
				count   float64
				invalid bool
			}{
				{request: &healthv1.EvaluateRequest{}, count: 3},
				{request: &healthv1.EvaluateRequest{XidFrom: proto.Int64(200)}, count: 1},
				{request: &healthv1.EvaluateRequest{XidTo: proto.Int64(200)}, count: 1},
				{request: &healthv1.EvaluateRequest{XidFrom: proto.Int64(250), XidTo: proto.Int64(300)}, count: 0},
				{request: &healthv1.EvaluateRequest{XidFrom: proto.Int64(0)}, count: 4},
				{request: &healthv1.EvaluateRequest{XidFrom: proto.Int64(300)}, invalid: true},
				{request: &healthv1.EvaluateRequest{XidTo: proto.Int64(100)}, invalid: true},
				{request: &healthv1.EvaluateRequest{}, count: 3},
			} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var verdict *healthv1.NodeVerdict
				if transport == "gRPC" {
					verdict, err = healthv1.NewVerdictServiceClient(conn).Evaluate(ctx, tc.request)
					cancel()
					if tc.invalid {
						if status.Code(err) != codes.InvalidArgument {
							t.Fatalf("request %v: %v", tc.request, err)
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
				} else {
					body, marshalErr := protojson.Marshal(tc.request)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					path := "http://" + address + "/verdict"
					if transport == "GET" {
						var query []string
						if tc.request.XidFrom != nil {
							query = append(query, fmt.Sprintf("xid_from=%d", tc.request.GetXidFrom()))
						}
						if tc.request.XidTo != nil {
							query = append(query, fmt.Sprintf("xid_to=%d", tc.request.GetXidTo()))
						}
						if len(query) > 0 {
							path += "?" + strings.Join(query, "&")
						}
						body = nil
					}
					req, requestErr := http.NewRequestWithContext(ctx, transport, path, bytes.NewReader(body))
					if requestErr != nil {
						cancel()
						t.Fatal(requestErr)
					}
					resp, requestErr := client.Do(req)
					if requestErr != nil {
						cancel()
						t.Fatal(requestErr)
					}
					data, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					cancel()
					if readErr != nil {
						t.Fatal(readErr)
					}
					if tc.invalid {
						if resp.StatusCode != http.StatusBadRequest {
							t.Fatalf("request %v: HTTP %d: %s", tc.request, resp.StatusCode, data)
						}
						continue
					}
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("HTTP %d: %s", resp.StatusCode, data)
					}
					verdict = &healthv1.NodeVerdict{}
					if decodeErr := protojson.Unmarshal(data, verdict); decodeErr != nil {
						t.Fatal(decodeErr)
					}
				}
				found := false
				for _, check := range verdict.CheckResults {
					if check.GetCheckId() != "gpu_xid_79" {
						continue
					}
					found = true
					counts := check.ObservedValues["xid_counts"].GetStructValue().GetFields()
					if got := counts["79"].GetNumberValue(); got != tc.count {
						t.Fatalf("request %v: count=%v, want %v (%v)", tc.request, got, tc.count, check)
					}
					want := healthv1.Severity_CRITICAL
					if tc.count == 0 {
						want = healthv1.Severity_PASS
					}
					if check.GetSeverity() != want {
						t.Fatalf("request %v: severity=%v, want %v", tc.request, check.GetSeverity(), want)
					}
				}
				if !found {
					t.Fatal("missing gpu_xid_79 result")
				}
			}
		})
	}
}
