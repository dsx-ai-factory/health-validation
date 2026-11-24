// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package verdictserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	healthzPath         = "/healthz"
	healthCheckMethod   = "/grpc.health.v1.Health/Check"
	livenessWatchPeriod = time.Second
)

// StuckCall names a source call that has not returned.
type StuckCall struct {
	Since  time.Time
	Source string
}

// Liveness is the process liveness state. It says nothing about node health
// or readiness to serve a verdict.
type Liveness struct {
	StuckCalls []StuckCall
	OK         bool
}

// LivenessFunc reports liveness. It must return promptly and must not wait on
// request admission, because probes must answer while a request is stuck.
type LivenessFunc func() Liveness

// WithLiveness serves GET /healthz and the standard gRPC health service
// (service name "") from check. Both bypass the in-flight request limit.
func WithLiveness(check LivenessFunc) Option {
	return func(options *verdictOptions) { options.liveness = check }
}

type livenessBody struct {
	Status     string          `json:"status"`
	StuckCalls []stuckCallBody `json:"stuck_calls,omitempty"`
}

type stuckCallBody struct {
	Source string `json:"source"`
	Since  string `json:"since"`
}

func healthzHandler(check LivenessFunc) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "only GET and HEAD are supported", http.StatusMethodNotAllowed)
			return
		}
		state := check()
		body := livenessBody{Status: "ok"}
		code := http.StatusOK
		if !state.OK {
			body.Status, code = "unhealthy", http.StatusServiceUnavailable
			for _, call := range state.StuckCalls {
				body.StuckCalls = append(body.StuckCalls, stuckCallBody{Source: call.Source, Since: call.Since.Format(time.RFC3339Nano)})
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(code)
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(writer).Encode(body)
		}
	})
}

// healthService answers on demand: the stuck-call deadline passing is not an
// event, so pushed statuses (grpc/health.Server) would go stale.
type healthService struct {
	healthgrpc.UnimplementedHealthServer
	check  LivenessFunc
	period time.Duration
}

func (service *healthService) servingStatus() healthgrpc.HealthCheckResponse_ServingStatus {
	if service.check().OK {
		return healthgrpc.HealthCheckResponse_SERVING
	}
	return healthgrpc.HealthCheckResponse_NOT_SERVING
}

func (service *healthService) Check(_ context.Context, request *healthgrpc.HealthCheckRequest) (*healthgrpc.HealthCheckResponse, error) {
	if request.GetService() != "" {
		return nil, status.Error(codes.NotFound, "unknown service")
	}
	return &healthgrpc.HealthCheckResponse{Status: service.servingStatus()}, nil
}

// Watch sends the current status, then polls and sends on each change.
func (service *healthService) Watch(request *healthgrpc.HealthCheckRequest, stream healthgrpc.Health_WatchServer) error {
	if request.GetService() != "" {
		return status.Error(codes.NotFound, "unknown service")
	}
	ticker := time.NewTicker(service.period)
	defer ticker.Stop()
	last := healthgrpc.HealthCheckResponse_UNKNOWN
	for {
		if current := service.servingStatus(); current != last {
			if err := stream.Send(&healthgrpc.HealthCheckResponse{Status: current}); err != nil {
				return status.Error(codes.Canceled, "stream closed")
			}
			last = current
		}
		select {
		case <-stream.Context().Done():
			return status.FromContextError(stream.Context().Err()).Err()
		case <-ticker.C:
		}
	}
}
