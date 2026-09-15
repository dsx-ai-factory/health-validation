package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"

	healthv1 "github.com/dsx-ai-factory/health-validation/collect/proto/go/health/v1"
)

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 2 * time.Minute
	httpIdleTimeout       = 2 * time.Minute
)

type checkService struct {
	healthv1.UnimplementedCheckServiceServer
	server *Server
}

func (s *checkService) Check(ctx context.Context, _ *healthv1.CheckRequest) (*healthv1.ObservationBatch, error) {
	slog.Debug("gRPC CheckService.Check")
	return s.server.Check(ctx)
}

// Serve exposes plaintext gRPC and HTTP on one TCP address. TLS is out of
// scope for the experimental module.
func Serve(address string, collector *Server) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	return serveOnListener(listener, collector)
}

func serveOnListener(listener net.Listener, collector *Server) error {
	mux := cmux.New(listener)
	mux.SetReadTimeout(httpReadHeaderTimeout)
	// Some HTTP/2 clients wait for the server's SETTINGS frame before sending
	// request headers. Send it while cmux reads the gRPC content type.
	grpcListener := mux.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"),
	)
	httpListener := mux.Match(cmux.HTTP1Fast())
	// cmux routes only the two protocols above. Plain HTTP/2 without the
	// gRPC content-type matches neither listener, and cmux returns an error
	// for that traffic. This prototype serves gRPC and HTTP/1 only.

	grpcServer := grpc.NewServer()
	healthv1.RegisterCheckServiceServer(grpcServer, &checkService{server: collector})
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("GET /check", func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("HTTP GET /check")
		batch, err := collector.Check(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out, err := protojson.Marshal(batch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(out)
	})
	httpServer := &http.Server{
		Handler:           httpMux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	// Each server runs on its own goroutine. When one stops, stop the
	// others so no goroutine or listener is left running.
	serveErrors := make(chan error, 3)
	go func() { serveErrors <- grpcServer.Serve(grpcListener) }()
	go func() { serveErrors <- httpServer.Serve(httpListener) }()
	go func() { serveErrors <- mux.Serve() }()

	firstErr := <-serveErrors
	grpcServer.GracefulStop()
	_ = httpServer.Shutdown(context.Background())
	_ = listener.Close()
	// Drain the remaining two servers so their goroutines exit.
	<-serveErrors
	<-serveErrors
	return firstErr
}
