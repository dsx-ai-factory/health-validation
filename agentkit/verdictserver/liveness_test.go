// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package verdictserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func startLivenessServer(t *testing.T, down *atomic.Bool, since time.Time, options ...Option) (string, healthgrpc.HealthClient) {
	t.Helper()
	check := func() Liveness {
		if down.Load() {
			return Liveness{StuckCalls: []StuckCall{{Source: "dcgm", Since: since}}}
		}
		return Liveness{OK: true}
	}
	address, _, _ := startVerdictServer(t, func(ctx context.Context, _ *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, append(options, WithLiveness(check))...)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	return address, healthgrpc.NewHealthClient(connection)
}

func TestHealthzReportsLiveness(t *testing.T) {
	var down atomic.Bool
	since := time.Date(2026, 11, 24, 1, 2, 3, 0, time.UTC)
	address, rpc := startLivenessServer(t, &down, since)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	get := func() (int, livenessBody) {
		response, err := http.Get("http://" + address + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var body livenessBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("body %q: %v", raw, err)
		}
		return response.StatusCode, body
	}
	if code, body := get(); code != http.StatusOK || body.Status != "ok" {
		t.Fatalf("healthy: %d %+v", code, body)
	}
	if got, err := rpc.Check(ctx, &healthgrpc.HealthCheckRequest{}); err != nil || got.Status != healthgrpc.HealthCheckResponse_SERVING {
		t.Fatalf("healthy Check: %v, %v", got, err)
	}

	down.Store(true)
	code, body := get()
	if code != http.StatusServiceUnavailable || len(body.StuckCalls) != 1 || body.StuckCalls[0].Source != "dcgm" || body.StuckCalls[0].Since != since.Format(time.RFC3339Nano) {
		t.Fatalf("unhealthy: %d %+v", code, body)
	}
	if got, err := rpc.Check(ctx, &healthgrpc.HealthCheckRequest{}); err != nil || got.Status != healthgrpc.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("unhealthy Check: %v, %v", got, err)
	}
	if _, err := rpc.Check(ctx, &healthgrpc.HealthCheckRequest{Service: "other"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown service: %v", err)
	}

	down.Store(false)
	if code, _ := get(); code != http.StatusOK {
		t.Fatalf("recovered: %d", code)
	}
}

func TestHealthzMethods(t *testing.T) {
	var down atomic.Bool
	address, _ := startLivenessServer(t, &down, time.Time{})
	response, err := http.Post("http://"+address+"/healthz", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", response.StatusCode)
	}
}

func TestHealthRoutesAbsentWithoutLiveness(t *testing.T) {
	address, _, _ := startVerdictServer(t, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return nil, nil })
	response, err := http.Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", response.StatusCode)
	}
}

func TestLivenessBypassesSaturatedLimiter(t *testing.T) {
	entered := make(chan struct{})
	address, _, _ := startVerdictServer(t, func(ctx context.Context, _ *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}, WithMaxInFlightRequests(1), WithLiveness(func() Liveness { return Liveness{OK: true} }))
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	rpc := healthgrpc.NewHealthClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	get := func(path string) int {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	// The first request holds the only slot until its context ends.
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/verdict", nil)
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("holding request never reached the handler")
	}
	if code := get("/verdict"); code != http.StatusTooManyRequests {
		t.Fatalf("/verdict under saturation: %d, want 429", code)
	}
	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz under saturation: %d", code)
	}
	if got, err := rpc.Check(ctx, &healthgrpc.HealthCheckRequest{}); err != nil || got.Status != healthgrpc.HealthCheckResponse_SERVING {
		t.Fatalf("Check under saturation: %v, %v", got, err)
	}
}

func TestHealthWatchSendsOnChange(t *testing.T) {
	var down atomic.Bool
	service := &healthService{period: time.Millisecond, check: func() Liveness { return Liveness{OK: !down.Load()} }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := &watchStream{ctx: ctx, sent: make(chan healthgrpc.HealthCheckResponse_ServingStatus, 4)}
	done := make(chan error, 1)
	go func() { done <- service.Watch(&healthgrpc.HealthCheckRequest{}, stream) }()
	if got := <-stream.sent; got != healthgrpc.HealthCheckResponse_SERVING {
		t.Fatalf("first=%v", got)
	}
	down.Store(true)
	if got := <-stream.sent; got != healthgrpc.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("second=%v", got)
	}
	cancel()
	if err := <-done; status.Code(err) != codes.Canceled {
		t.Fatalf("Watch end: %v", err)
	}
}

func TestHealthWatchUnknownServiceStaysOpen(t *testing.T) {
	service := &healthService{period: time.Millisecond, check: func() Liveness { return Liveness{OK: true} }}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{})
	ctx := &doneSignalContext{Context: base, signal: waiting}
	stream := &watchStream{ctx: ctx, sent: make(chan healthgrpc.HealthCheckResponse_ServingStatus, 4)}
	done := make(chan error, 1)
	go func() { done <- service.Watch(&healthgrpc.HealthCheckRequest{Service: "other"}, stream) }()
	select {
	case got := <-stream.sent:
		if got != healthgrpc.HealthCheckResponse_SERVICE_UNKNOWN {
			t.Fatalf("status=%v, want SERVICE_UNKNOWN", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not send SERVICE_UNKNOWN")
	}
	// Watch must reach its cancellation wait; an early return fails here.
	select {
	case <-waiting:
	case err := <-done:
		t.Fatalf("Watch returned before waiting for cancellation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Watch never waited for cancellation")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("Watch end: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after cancellation")
	}
}

// doneSignalContext closes signal the first time Done is read.
type doneSignalContext struct {
	context.Context
	signal chan struct{}
	once   sync.Once
}

func (c *doneSignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.signal) })
	return c.Context.Done()
}

type watchStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan healthgrpc.HealthCheckResponse_ServingStatus
}

func (stream *watchStream) Context() context.Context { return stream.ctx }
func (stream *watchStream) Send(response *healthgrpc.HealthCheckResponse) error {
	stream.sent <- response.Status
	return nil
}
