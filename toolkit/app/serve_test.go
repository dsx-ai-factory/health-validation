// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/agentkit/server"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
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
		done <- server.ServeListener(ctx, listener, agent.evaluate, server.WithAfterResponse(agent.afterResponse))
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
	body, err = verdictproto.MarshalJSON(verdict)
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
	} {
		if _, err := agent.evaluate(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Errorf("request %v: %v", request, err)
		}
	}
}

type blockingAgentGPU struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (backend *blockingAgentGPU) MeasureGPUs() ([]health.Measurement, error) {
	backend.calls.Add(1)
	select {
	case backend.started <- struct{}{}:
	default:
	}
	<-backend.release
	return nil, nil
}

func TestAgentKeepsTimedOutNativeWorkSerialized(t *testing.T) {
	backend := &blockingAgentGPU{started: make(chan struct{}, 2), release: make(chan struct{})}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	a := cadenceRunApp().WithGPUBackend(backend)
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
	queued, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	if _, err := agent.evaluate(queued, &healthv1.EvaluateRequest{}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("queued request = %v", err)
	}
	if got := backend.calls.Load(); got != 1 {
		t.Fatalf("overlapping native collection: %d calls", got)
	}
	close(backend.release)
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
	a := cadenceRunApp().WithGPUBackend(toolkit.MockGPU(gpu.GPUMeasurement{GPUID: "GPU-duplicate"}, gpu.GPUMeasurement{GPUID: "GPU-duplicate"}))
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	if _, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("ambiguous measurement = %v", err)
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
	backend := &blockingAgentGPU{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(backend.release)
	a := cadenceRunApp().WithGPUBackend(backend)
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
		{"--cadence", "invalid"},
		{"--node-id", " "},
		{"--execution-context", "invalid"},
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
