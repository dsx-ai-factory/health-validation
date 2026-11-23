// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package verdictserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
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
	if err := protojson.Unmarshal([]byte(`{"schema_version":"0.7","catalog_version":"test","timestamp":"2026-11-02T00:00:00Z","trace_id":"trace","overall_severity":"CRITICAL","severity_by_domain":{"gpu":"CRITICAL"},"check_results":[]}`), verdict); err != nil {
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
		if request.GetNodeId() != "node-a" || request.XidFrom == nil || request.GetXidFrom() != 0 || request.GetXidTo() != 200 {
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
	if decodeErr := protojson.Unmarshal([]byte(`{"node_id":"node-a","xid_from":"0","xid_to":"200"}`), request); decodeErr != nil {
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
		path, body := "/verdict?node_id=node-a&xid_from=0&xid_to=200", ""
		if method == http.MethodPost {
			path, body = "/verdict", `{"node_id":"node-a","xid_from":"0","xid_to":"200"}`
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
		{http.MethodGet, "/verdict?xid_from=1&xid_from=2", ""},
		{http.MethodGet, "/verdict?xid_to=1&xid_to=2", ""},
		{http.MethodGet, "/verdict?xid_from=", ""},
		{http.MethodGet, "/verdict?xid_to=oops", ""},
		{http.MethodGet, "/verdict?xid_from=1.5", ""},
		{http.MethodGet, "/verdict?xid_to=9223372036854775808", ""},
		{http.MethodPost, "/verdict", `{"xid_from":"abc"}`},
		{http.MethodPost, "/verdict", `{"xid_to":1.5}`},
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

type readNotifyingListener struct {
	net.Listener
	reading chan struct{}
}

func (listener readNotifyingListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return readNotifyingConn{Conn: connection, reading: listener.reading}, nil
}

type readNotifyingConn struct {
	net.Conn
	reading chan struct{}
}

func (connection readNotifyingConn) Read(buffer []byte) (int, error) {
	select {
	case connection.reading <- struct{}{}:
	default:
	}
	return connection.Conn.Read(buffer)
}

func TestVerdictShutdownBoundsIdleUnclassifiedConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	reading := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- ServeListener(ctx, readNotifyingListener{Listener: listener, reading: reading}, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
			return nil, nil
		}, WithShutdownTimeout(50*time.Millisecond))
	}()
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start reading the idle connection")
	}
	cancel()
	select {
	case shutdownErr := <-stopped:
		if shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	case <-time.After(time.Second):
		t.Fatal("idle connection kept shutdown running past its deadline")
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

func TestVerdictHTTPPreservesRequiredEmptyComponentChecks(t *testing.T) {
	verdict := testVerdict(t)
	verdict.AffectedComponents = []*healthv1.AffectedComponent{{Category: healthv1.Category_gpu.Enum(), MaxSeverity: healthv1.Severity_CRITICAL.Enum()}}
	handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return verdict, nil }}, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/verdict", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"checks":[]`) {
		t.Fatalf("required empty checks omitted: %s", response.Body.String())
	}
	back := &healthv1.NodeVerdict{}
	if err := protojson.Unmarshal(response.Body.Bytes(), back); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(verdict, back) {
		t.Fatalf("HTTP roundtrip changed verdict: got=%v want=%v", back, verdict)
	}
}

// pipeResponseWriter writes to a client that deliberately never reads.
type pipeResponseWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *pipeResponseWriter) SetWriteDeadline(deadline time.Time) error {
	return w.conn.SetWriteDeadline(deadline)
}

func (w *pipeResponseWriter) Write(data []byte) (int, error) { return w.conn.Write(data) }

func TestVerdictWriteDeadlineStartsAfterEvaluation(t *testing.T) {
	for name, evaluationErr := range map[string]error{"success": nil, "evaluation error": errors.New("evaluation failed")} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				defer server.Close()
				defer client.Close()
				writer := &pipeResponseWriter{ResponseRecorder: httptest.NewRecorder(), conn: server}
				want := testVerdict(t)
				handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
					time.Sleep(3 * time.Minute)
					return want, evaluationErr
				}}, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
					t.Error("failed delivery published telemetry")
				})
				started := time.Now()
				handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/verdict", nil))
				if elapsed := time.Since(started); elapsed != 3*time.Minute+httpWriteTimeout {
					t.Fatalf("request took %s; want evaluation plus delivery timeout", elapsed)
				}
			})
		})
	}
}

func TestDecodeErrorsBoundEchoedInput(t *testing.T) {
	handler := verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		t.Error("invalid request started evaluation")
		return nil, nil
	}}, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
		t.Error("invalid request published telemetry")
	})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for name, field := range map[string]string{
			"short":        "unknown_field",
			"moderate":     strings.Repeat("a", 1024),
			"oversized":    strings.Repeat("a", 1<<16),
			"escape heavy": strings.Repeat("\x00\"\\", 1<<14),
			"multibyte":    strings.Repeat("界", 1<<15),
		} {
			t.Run(method+"/"+name, func(t *testing.T) {
				encoded, err := json.Marshal(map[string]bool{field: true})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(method, "/verdict", bytes.NewReader(encoded))
				if method == http.MethodGet {
					request = httptest.NewRequest(method, "/verdict?"+url.QueryEscape(field)+"=true", nil)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest || response.Body.Len() > 32<<10 {
					t.Fatalf("status=%d, response bytes=%d", response.Code, response.Body.Len())
				}
				var result map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				message := result["error"]
				if message == "" || len(message) > maxDecodeErrorBytes {
					t.Fatalf("error message bytes=%d", len(message))
				}
				if (name == "short" || name == "moderate") && !strings.Contains(message, field) {
					t.Fatalf("diagnostic lost field name: %s", message)
				}
			})
		}
	}
}

func TestDecodeErrorDeliveryTimeoutReleasesAdmission(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				defer server.Close()
				defer client.Close()
				writer := &pipeResponseWriter{ResponseRecorder: httptest.NewRecorder(), conn: server}
				limit := make(requestLimit, 1)
				calls, publications := 0, 0
				want := testVerdict(t)
				handler := limit.httpHandler(verdictHTTPHandler(&verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
					calls++
					return want, nil
				}}, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) { publications++ }))
				request := httptest.NewRequest(method, "/verdict", strings.NewReader(`{"unknown":true}`))
				if method == http.MethodGet {
					request = httptest.NewRequest(method, "/verdict?unknown=true", nil)
				}
				done := make(chan struct{})
				started := time.Now()
				go func() {
					handler.ServeHTTP(writer, request)
					close(done)
				}()
				synctest.Wait()
				if writer.Code != http.StatusBadRequest || len(limit) != 1 {
					t.Fatalf("status=%d, outstanding=%d", writer.Code, len(limit))
				}
				overloaded := httptest.NewRecorder()
				handler.ServeHTTP(overloaded, httptest.NewRequest(http.MethodGet, "/verdict", nil))
				if overloaded.Code != http.StatusTooManyRequests {
					t.Fatalf("blocked delivery did not hold admission: status=%d", overloaded.Code)
				}
				<-done
				if elapsed := time.Since(started); elapsed != httpWriteTimeout {
					t.Fatalf("decode-error delivery took %s, want %s", elapsed, httpWriteTimeout)
				}
				if len(limit) != 0 || calls != 0 || publications != 0 {
					t.Fatalf("decode error: outstanding=%d evaluations=%d publications=%d", len(limit), calls, publications)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/verdict", nil))
				if response.Code != http.StatusOK || calls != 1 || publications != 1 || len(limit) != 0 {
					t.Fatalf("capacity did not recover: status=%d evaluations=%d publications=%d outstanding=%d", response.Code, calls, publications, len(limit))
				}
			})
		})
	}
}

func TestVerdictHTTPShutdownCancelsActiveRequest(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	grpcEntered := make(chan struct{})
	address, cancel, stopped := startVerdictServer(t, func(ctx context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		if request.GetNodeId() == "grpc" {
			close(grpcEntered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}, WithShutdownTimeout(20*time.Millisecond))
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	grpcDone := make(chan struct{})
	go func() {
		defer close(grpcDone)
		_, _ = healthv1.NewVerdictServiceClient(connection).Evaluate(ctx, &healthv1.EvaluateRequest{NodeId: proto.String("grpc")})
	}()
	select {
	case <-grpcEntered:
	case <-ctx.Done():
		t.Fatal("gRPC handler did not start")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/verdict", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		response, callErr := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		finished <- callErr
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP handler did not start")
	}
	cancel()
	select {
	case shutdownErr := <-stopped:
		if shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not finish")
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("shutdown left the active HTTP handler running")
	}
	select {
	case <-grpcDone:
	case <-ctx.Done():
		t.Fatal("shutdown left the gRPC client waiting")
	}
	select {
	case callErr := <-finished:
		if callErr == nil {
			t.Fatal("shutdown left the active HTTP connection open")
		}
	case <-ctx.Done():
		t.Fatal("shutdown left the HTTP client waiting")
	}
}

func TestVerdictShutdownClosesActiveHTTPConnection(t *testing.T) {
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	address, cancel, stopped := startVerdictServer(t, func(ctx context.Context, _ *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		entered <- ctx
		<-release
		return nil, context.Canceled
	}, WithShutdownTimeout(30*time.Millisecond))
	requestCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/verdict", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	var handlerCtx context.Context
	select {
	case handlerCtx = <-entered:
	case <-requestCtx.Done():
		t.Fatal("HTTP handler did not start")
	}
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-requestCtx.Done():
		t.Fatal("shutdown did not finish")
	}
	select {
	case <-handlerCtx.Done():
	case <-requestCtx.Done():
		t.Fatal("shutdown left HTTP connection open")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted HTTP request succeeded")
		}
	case <-requestCtx.Done():
		t.Fatal("HTTP client did not finish")
	}
}

func grpcClassificationInput(t *testing.T) []byte {
	t.Helper()
	var input, headers bytes.Buffer
	input.WriteString(http2.ClientPreface)
	framer := http2.NewFramer(&input, nil)
	if err := framer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	encoder := hpack.NewEncoder(&headers)
	if err := encoder.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"}); err != nil {
		t.Fatal(err)
	}
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: headers.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	return input.Bytes()
}

func TestGRPCClassificationBoundsSettingsWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		// cmux's read deadline cannot unblock a write to a non-reading peer.
		if err := server.SetReadDeadline(time.Now().Add(httpReadHeaderTimeout)); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if matchGRPCWithWriteDeadline(server, bytes.NewReader(grpcClassificationInput(t))) {
			t.Fatal("classified connection whose peer never reads SETTINGS")
		}
		if elapsed := time.Since(started); elapsed != httpReadHeaderTimeout {
			t.Fatalf("classification took %s, want %s", elapsed, httpReadHeaderTimeout)
		}
	})
}

func TestClassificationClearsWriteDeadlineForBothProtocols(t *testing.T) {
	for _, protocol := range []string{"gRPC", "HTTP"} {
		t.Run(protocol, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				defer server.Close()
				defer client.Close()
				input := []byte("GET /verdict HTTP/1.1\r\nHost: localhost\r\n\r\n")
				if protocol == "gRPC" {
					input = grpcClassificationInput(t)
					go func() {
						frame, err := http2.NewFramer(nil, client).ReadFrame()
						if err != nil {
							t.Error(err)
							return
						}
						if _, ok := frame.(*http2.SettingsFrame); !ok {
							t.Errorf("frame = %T, want SETTINGS", frame)
						}
					}()
				}
				if got := matchGRPCWithWriteDeadline(server, bytes.NewReader(input)); got != (protocol == "gRPC") {
					t.Fatalf("gRPC classification = %v for %s", got, protocol)
				}
				// Response delivery must remain possible after a longer evaluation.
				time.Sleep(httpReadHeaderTimeout + time.Second)
				received := make(chan error, 1)
				go func() {
					var data [1]byte
					_, err := io.ReadFull(client, data[:])
					received <- err
				}()
				if _, err := server.Write([]byte{1}); err != nil {
					t.Fatalf("classification left a write deadline: %v", err)
				}
				if err := <-received; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
