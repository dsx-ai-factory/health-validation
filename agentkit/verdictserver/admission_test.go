// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package verdictserver

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
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func TestRequestCapacityIsSharedAcrossProtocols(t *testing.T) {
	for _, first := range []string{"HTTP", "gRPC"} {
		t.Run(first, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			want := testVerdict(t)
			address, _, _ := startVerdictServer(t, func(ctx context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
				if request.GetNodeId() == "hold" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return want, nil
			}, WithMaxInFlightRequests(1))
			connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rpc := healthv1.NewVerdictServiceClient(connection)
			client := &http.Client{Timeout: 5 * time.Second}
			done := make(chan error, 1)
			go func() {
				if first == "gRPC" {
					_, callErr := rpc.Evaluate(ctx, &healthv1.EvaluateRequest{NodeId: proto.String("hold")})
					done <- callErr
				} else {
					req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/verdict?node_id=hold", nil)
					if requestErr != nil {
						done <- requestErr
						return
					}
					response, callErr := client.Do(req)
					if response != nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
					done <- callErr
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first request did not start")
			}
			response, err := client.Get("http://" + address + "/verdict")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("HTTP status=%d, want 429", response.StatusCode)
			}
			if _, err := rpc.Evaluate(ctx, &healthv1.EvaluateRequest{}); status.Code(err) != codes.ResourceExhausted {
				t.Fatalf("gRPC code=%v, want RESOURCE_EXHAUSTED", status.Code(err))
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("canceled request did not return")
			}
			// RPC stream cleanup can finish after the client observes cancellation.
			deadline := time.Now().Add(5 * time.Second)
			for {
				response, err := client.Get("http://" + address + "/verdict")
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					break
				}
				if response.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
					t.Fatalf("capacity did not recover: HTTP %d", response.StatusCode)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

type unreadableAdmissionBody struct{ t *testing.T }

func (b unreadableAdmissionBody) Read([]byte) (int, error) {
	b.t.Error("overloaded request body was read")
	return 0, errors.New("unexpected read")
}

func TestOverloadedHTTPDoesNotDecodeOrEvaluate(t *testing.T) {
	limit := make(requestLimit, 1)
	limit.acquire()
	defer func() { <-limit }()
	service := &verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		t.Error("overloaded request evaluated")
		return nil, nil
	}}
	handler := limit.httpHandler(verdictHTTPHandler(service, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
		t.Error("overloaded request published telemetry")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/verdict", unreadableAdmissionBody{t}))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", response.Code)
	}
}

func TestHTTPAdmissionReleasedOnDecodeAndHandlerErrors(t *testing.T) {
	limit := make(requestLimit, 1)
	service := &verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
		return nil, errors.New("evaluation failed")
	}}
	handler := limit.httpHandler(verdictHTTPHandler(service, nil))
	for _, body := range []string{`{`, `{}`} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/verdict", strings.NewReader(body)))
		if len(limit) != 0 {
			t.Fatal("failed request leaked admission capacity")
		}
	}
}

func TestGRPCAdmissionHeldThroughDeliveryAndTelemetry(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled, errors.New("delivery failed")} {
		limit := make(requestLimit, 1)
		handler := &responseStats{after: func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
			if len(limit) != 1 {
				t.Error("capacity released before telemetry completed")
			}
		}}
		ctx := handler.TagRPC(context.Background(), &stats.RPCTagInfo{})
		state := ctx.Value(responseKey{}).(*responseState)
		_, err := limit.intercept(ctx, &healthv1.EvaluateRequest{}, nil, func(context.Context, any) (any, error) {
			state.verdict = testVerdict(t)
			return state.verdict, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(limit) != 1 {
			t.Fatal("capacity released before response delivery")
		}
		handler.HandleRPC(ctx, &stats.End{Error: failure})
		if len(limit) != 0 {
			t.Fatal("RPC completion leaked admission capacity")
		}
	}
}

func TestInvalidRequestCapacityRejected(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		err = ServeListener(context.Background(), listener, func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return nil, nil }, WithMaxInFlightRequests(capacity))
		if err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Fatalf("limit=%d: %v", capacity, err)
		}
	}
}

func TestHTTPAdmissionHeldThroughTelemetry(t *testing.T) {
	limit := make(requestLimit, 1)
	want := testVerdict(t)
	service := &verdictService{handler: func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) { return want, nil }}
	published := false
	handler := limit.httpHandler(verdictHTTPHandler(service, func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict) {
		published = true
		if len(limit) != 1 {
			t.Error("capacity released before telemetry completed")
		}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/verdict", nil))
	if response.Code != http.StatusOK || !published || len(limit) != 0 {
		t.Fatalf("HTTP status=%d, telemetry=%v, outstanding=%d", response.Code, published, len(limit))
	}
}
