// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/proto/verdictjson"
)

// Handler evaluates a request. Every produced verdict is a successful response,
// including a verdict whose overall severity is CRITICAL or UNKNOWN.
type Handler func(context.Context, *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error)

// AfterResponse runs after a verdict has been written successfully. It must
// bound its own work; the request context may already have been canceled.
type AfterResponse func(context.Context, *healthv1.EvaluateRequest, *healthv1.NodeVerdict)

type verdictOptions struct {
	afterResponse   AfterResponse
	shutdownTimeout time.Duration
}

type Option func(*verdictOptions)

func WithAfterResponse(callback AfterResponse) Option {
	return func(options *verdictOptions) { options.afterResponse = callback }
}

// WithShutdownTimeout bounds the time allowed for requests to finish at shutdown.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(options *verdictOptions) {
		if timeout > 0 {
			options.shutdownTimeout = timeout
		}
	}
}

// ServeContext serves plaintext gRPC and HTTP verdict requests on one address.
func ServeContext(ctx context.Context, address string, handler Handler, options ...Option) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	return ServeListener(ctx, listener, handler, options...)
}

// ServeListener takes ownership of listener and closes it before returning.
func ServeListener(ctx context.Context, listener net.Listener, handler Handler, options ...Option) error {
	defer listener.Close()
	if handler == nil {
		return errors.New("verdict handler is required")
	}
	settings := verdictOptions{shutdownTimeout: 5 * time.Second}
	for _, option := range options {
		option(&settings)
	}
	service := &verdictService{handler: handler}
	multiplexer := cmux.New(listener)
	multiplexer.SetReadTimeout(httpReadHeaderTimeout)
	grpcListener := multiplexer.MatchWithWriters(cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"))
	httpListener := multiplexer.Match(cmux.HTTP1Fast())
	grpcServer := grpc.NewServer(grpc.StatsHandler(&responseStats{after: settings.afterResponse}))
	healthv1.RegisterVerdictServiceServer(grpcServer, service)
	httpServer := &http.Server{
		Handler:           verdictHTTPHandler(service, settings.afterResponse),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		// The host bounds evaluation with its request context; a fixed write
		// timeout would prematurely cut off a longer configured collection.
		WriteTimeout: 0,
		IdleTimeout:  httpIdleTimeout,
	}
	serveErrors := make(chan error, 3)
	go func() { serveErrors <- grpcServer.Serve(grpcListener) }()
	go func() { serveErrors <- httpServer.Serve(httpListener) }()
	go func() { serveErrors <- multiplexer.Serve() }()

	var firstErr error
	consumed := 0
	select {
	case <-ctx.Done():
	case firstErr = <-serveErrors:
		consumed++
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), settings.shutdownTimeout)
	defer cancel()
	grpcStopped := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(grpcStopped) }()
	httpStopped := make(chan struct{})
	go func() { _ = httpServer.Shutdown(shutdownCtx); close(httpStopped) }()
	select {
	case <-grpcStopped:
	case <-shutdownCtx.Done():
		// GracefulStop may still be waiting for a handler that ignores its
		// context. Stop must not make the shutdown deadline unbounded.
		go grpcServer.Stop()
	}
	select {
	case <-httpStopped:
	case <-shutdownCtx.Done():
		_ = httpServer.Close()
		<-httpStopped
	}
	// cmux may still be blocked accepting a new connection after both child
	// servers stopped, so close its listener before draining their results.
	_ = listener.Close()
	if shutdownCtx.Err() == nil {
		for consumed < 3 {
			<-serveErrors
			consumed++
		}
	} else {
		_ = httpServer.Close()
	}
	if ctx.Err() != nil || errors.Is(firstErr, net.ErrClosed) || errors.Is(firstErr, http.ErrServerClosed) || errors.Is(firstErr, grpc.ErrServerStopped) {
		return nil
	}
	return firstErr
}

type verdictService struct {
	healthv1.UnimplementedVerdictServiceServer
	handler Handler
}

func (service *verdictService) Evaluate(ctx context.Context, request *healthv1.EvaluateRequest) (*healthv1.NodeVerdict, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	verdict, err := service.handler(ctx, request)
	if err != nil {
		return nil, transportError(err)
	}
	if verdict == nil {
		return nil, status.Error(codes.Internal, "evaluation produced no verdict")
	}
	if response, ok := ctx.Value(responseKey{}).(*responseState); ok {
		response.request, response.verdict = request, verdict
	}
	return verdict, nil
}

func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "request canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}

type (
	responseKey   struct{}
	responseState struct {
		request *healthv1.EvaluateRequest
		verdict *healthv1.NodeVerdict
	}
)
type responseStats struct{ after AfterResponse }

func (handler *responseStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return context.WithValue(ctx, responseKey{}, &responseState{})
}

func (handler *responseStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if end, ok := event.(*stats.End); ok && end.Error == nil && handler.after != nil {
		if response, ok := ctx.Value(responseKey{}).(*responseState); ok && response.verdict != nil {
			handler.after(ctx, response.request, response.verdict)
		}
	}
}
func (*responseStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (*responseStats) HandleConn(context.Context, stats.ConnStats)                       {}

func verdictHTTPHandler(service *verdictService, after AfterResponse) http.Handler {
	mux := http.NewServeMux()
	evaluate := func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodPost {
			writer.Header().Set("Allow", "GET, POST")
			http.Error(writer, "only GET and POST are supported", http.StatusMethodNotAllowed)
			return
		}
		input, err := decodeEvaluateRequest(writer, request)
		if err != nil {
			writeHTTPError(writer, err)
			return
		}
		verdict, err := service.Evaluate(request.Context(), input)
		if err != nil {
			writeHTTPError(writer, err)
			return
		}
		out, err := marshalVerdict(verdict)
		if err != nil {
			writeHTTPError(writer, status.Error(codes.Internal, "encode verdict"))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Length", strconv.Itoa(len(out)))
		if _, writeErr := writer.Write(out); writeErr != nil {
			return
		}
		// Flush before telemetry; otherwise net/http can buffer the response
		// until the metrics export finishes.
		if flushErr := http.NewResponseController(writer).Flush(); flushErr != nil {
			return
		}
		if after != nil {
			after(request.Context(), input, verdict)
		}
	}
	mux.HandleFunc("/verdict", evaluate)
	return mux
}

func decodeEvaluateRequest(writer http.ResponseWriter, request *http.Request) (*healthv1.EvaluateRequest, error) {
	input := &healthv1.EvaluateRequest{}
	var body []byte
	if request.Method == http.MethodGet {
		values, err := urlQuery(request)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		body, err = json.Marshal(values)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid query")
		}
	} else {
		if request.URL.RawQuery != "" {
			return nil, status.Error(codes.InvalidArgument, "POST overrides belong in the request body")
		}
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "request body exceeds 1 MiB or could not be read")
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			body = []byte("{}")
		}
	}
	if err := (protojson.UnmarshalOptions{}).Unmarshal(body, input); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid evaluation request: "+err.Error())
	}
	return input, nil
}

func urlQuery(request *http.Request) (map[string]string, error) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid request query: %w", err)
	}
	values := make(map[string]string, len(query))
	for name, entries := range query {
		if name != "cadence" && name != "execution_context" && name != "node_id" {
			return nil, fmt.Errorf("unknown request parameter %q", name)
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("request parameter %q must occur once", name)
		}
		values[name] = entries[0]
	}
	return values, err
}

func marshalVerdict(verdict *healthv1.NodeVerdict) ([]byte, error) {
	return verdictjson.Marshal(verdict)
}

func writeHTTPError(writer http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch status.Code(err) {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.Canceled:
		code = http.StatusRequestTimeout
	case codes.DeadlineExceeded:
		code = http.StatusGatewayTimeout
	case codes.Unavailable:
		code = http.StatusServiceUnavailable
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(code)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": status.Convert(err).Message()})
}
