// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func testVerdict(t *testing.T) *healthv1.NodeVerdict {
	t.Helper()
	verdict := &healthv1.NodeVerdict{}
	if err := protojson.Unmarshal([]byte(`{"schema_version":"0.4","catalog_version":"test","timestamp":"2026-11-02T00:00:00Z","trace_id":"trace","overall_severity":"CRITICAL","severity_by_domain":{"gpu":"CRITICAL"},"check_results":[]}`), verdict); err != nil {
		t.Fatal(err)
	}
	return verdict
}

func startVerdictServer(t *testing.T, handler Handler, options ...Option) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- ServeListener(ctx, listener, handler, options...) }()
	t.Cleanup(func() { cancel(); _ = listener.Close() })
	return listener.Addr().String(), cancel, stopped
}

func TestVerdictServesHTTPAndGRPCOnOnePort(t *testing.T) {
	want := testVerdict(t)
	address, cancel, stopped := startVerdictServer(t, func(_ context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		if request.GetNodeId() != "node-a" {
			return nil, status.Error(codes.InvalidArgument, "node_id must be node-a")
		}
		return want, nil
	})
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request := &healthv1.EvaluateRequest{}
	if decodeErr := protojson.Unmarshal([]byte(`{"node_id":"node-a"}`), request); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	got, err := healthv1.NewVerdictServiceClient(connection).Evaluate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("gRPC verdict = %v, want %v", got, want)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path, body := "/verdict?node_id=node-a", ""
		if method == http.MethodPost {
			path, body = "/verdict", `{"node_id":"node-a"}`
		}
		httpRequest, requestErr := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, callErr := client.Do(httpRequest)
		if callErr != nil {
			t.Fatal(callErr)
		}
		out, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", method, response.StatusCode, out)
		}
		parsed := &healthv1.NodeVerdict{}
		if decodeErr := protojson.Unmarshal(out, parsed); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if !proto.Equal(parsed, want) {
			t.Fatalf("HTTP verdict = %v, want %v", parsed, want)
		}
	}
	cancel()
	select {
	case shutdownErr := <-stopped:
		if shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestVerdictHTTPRejectsMalformedRequestsBeforeEvaluation(t *testing.T) {
	calls := 0
	handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		calls++
		return nil, nil
	}}, nil)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/verdict?unknown=x", ""},
		{http.MethodGet, "/verdict?node_id=a&node_id=b", ""},
		{http.MethodGet, "/verdict?node_id=%zz", ""},
		{http.MethodGet, "/verdict?cadence=TYPO", ""},
		{http.MethodPost, "/verdict", `{"unknown":1}`},
		{http.MethodPost, "/verdict", `{} {}`},
		{http.MethodPost, "/verdict?node_id=a", `{}`},
		{http.MethodPost, "/verdict", strings.Repeat(" ", (1<<20)+1)},
	} {
		t.Run(test.path+test.body[:min(len(test.body), 20)], func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("handler called %d times for invalid requests", calls)
	}
}

func TestVerdictErrorMappingAndNoTelemetryOnFailure(t *testing.T) {
	for _, test := range []struct {
		err      error
		code     codes.Code
		httpCode int
	}{
		{errors.New("failed"), codes.Internal, http.StatusInternalServerError},
		{context.Canceled, codes.Canceled, http.StatusRequestTimeout},
		{context.DeadlineExceeded, codes.DeadlineExceeded, http.StatusGatewayTimeout},
		{status.Error(codes.Unavailable, "offline"), codes.Unavailable, http.StatusServiceUnavailable},
		{status.Error(codes.InvalidArgument, "bad override"), codes.InvalidArgument, http.StatusBadRequest},
		{nil, codes.Internal, http.StatusInternalServerError},
	} {
		service := &verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return nil, test.err }}
		_, err := service.Evaluate(context.Background(), &healthv1.EvaluateRequest{})
		if status.Code(err) != test.code {
			t.Errorf("code = %v, want %v", status.Code(err), test.code)
		}
		response := httptest.NewRecorder()
		verdictHTTPHandler(service, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
			t.Error("telemetry called without successful response")
		}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/verdict", nil))
		if response.Code != test.httpCode {
			t.Errorf("HTTP code = %d, want %d", response.Code, test.httpCode)
		}
	}
}

func TestVerdictHTTPFlushesBeforeTelemetry(t *testing.T) {
	want := testVerdict(t)
	out, err := marshalVerdict(want)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	address, _, _ := startVerdictServer(t, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return want, nil }, WithAfterResponse(func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) { close(entered); <-release }))
	defer close(release)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/verdict")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("telemetry was not called")
	}
	buffer, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(buffer) != string(out) {
		t.Fatalf("response = %s, want %s", buffer, out)
	}
}

func TestVerdictShutdownBoundsUncooperativeHandler(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	address, cancel, stopped := startVerdictServer(t, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		close(entered)
		<-release
		return nil, context.Canceled
	}, WithShutdownTimeout(30*time.Millisecond))
	defer close(release)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	requestDone := make(chan error, 1)
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	go func() {
		_, rpcErr := healthv1.NewVerdictServiceClient(connection).Evaluate(ctx, &healthv1.EvaluateRequest{})
		requestDone <- rpcErr
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case shutdownErr := <-stopped:
		if shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited indefinitely for handler")
	}
	select {
	case rpcErr := <-requestDone:
		if rpcErr == nil {
			t.Fatal("canceled RPC succeeded")
		}
	case <-ctx.Done():
		t.Fatal("client did not finish")
	}
}

func TestVerdictGRPCCompletesBeforeTelemetry(t *testing.T) {
	want := testVerdict(t)
	entered, release := make(chan struct{}), make(chan struct{})
	address, _, _ := startVerdictServer(t, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return want, nil }, WithAfterResponse(func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) { close(entered); <-release }))
	defer close(release)
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
	if !proto.Equal(verdict, want) {
		t.Fatalf("verdict = %v", verdict)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("post-response callback did not run")
	}
}

func TestVerdictHTTPCancellationReachesHandler(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	address, _, _ := startVerdictServer(t, func(ctx context.Context, _ *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/verdict", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		response, callErr := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		finished <- callErr
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not receive cancellation")
	}
	select {
	case clientErr := <-finished:
		if !errors.Is(clientErr, context.Canceled) {
			t.Fatalf("client error = %v", clientErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP client did not finish")
	}
}

func TestVerdictGRPCErrorsDoNotPublishTelemetry(t *testing.T) {
	for _, expected := range []codes.Code{codes.Internal, codes.InvalidArgument, codes.Unavailable, codes.Canceled} {
		t.Run(expected.String(), func(t *testing.T) {
			published := make(chan struct{}, 1)
			address, cancel, stopped := startVerdictServer(t, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
				return nil, status.Error(expected, "evaluation failed")
			}, WithAfterResponse(func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) { published <- struct{}{} }))
			connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, rpcErr := healthv1.NewVerdictServiceClient(connection).Evaluate(ctx, &healthv1.EvaluateRequest{}); status.Code(rpcErr) != expected {
				t.Fatalf("RPC code = %v, want %v", status.Code(rpcErr), expected)
			}
			cancel()
			select {
			case shutdownErr := <-stopped:
				if shutdownErr != nil {
					t.Fatal(shutdownErr)
				}
			case <-ctx.Done():
				t.Fatal("server did not stop")
			}
			select {
			case <-published:
				t.Fatal("failed RPC published telemetry")
			default:
			}
		})
	}
}

func TestVerdictRejectsUnsupportedMethodsWithoutCollection(t *testing.T) {
	handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		t.Error("unsupported HTTP method started evaluation")
		return nil, nil
	}}, nil)
	for _, method := range []string{http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/verdict", nil))
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, POST" {
			t.Errorf("%s status=%d allow=%q", method, response.Code, response.Header().Get("Allow"))
		}
	}
}

func TestVerdictHTTPPreservesRequiredEmptyComponentCheckIDs(t *testing.T) {
	verdict := testVerdict(t)
	verdict.AffectedComponents = []*healthv1.AffectedComponent{{Category: healthv1.Category_gpu.Enum(), MaxSeverity: healthv1.Severity_CRITICAL.Enum()}}
	handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return verdict, nil }}, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/verdict", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"check_ids":[]`) {
		t.Fatalf("required empty check_ids omitted: %s", response.Body.String())
	}
	back := &healthv1.NodeVerdict{}
	if err := protojson.Unmarshal(response.Body.Bytes(), back); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(verdict, back) {
		t.Fatalf("HTTP roundtrip changed verdict: got=%v want=%v", back, verdict)
	}
}
