// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	healthv1 "github.com/dsx-ai-factory/health-validation/collect/proto/go/health/v1"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type testSensor struct {
	measure func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error)
	id      string
	signals collect.Signals
}

func (sensor testSensor) ID() string { return sensor.id }
func (sensor testSensor) Signals() collect.Signals {
	return sensor.signals
}

func (sensor testSensor) Measure(ctx context.Context, options ...collect.OpOption) (*observation.ObservationBatch, error) {
	return sensor.measure(ctx, options...)
}

func TestCheckAttributesTopLevelErrorsToSensor(t *testing.T) {
	collector := New()
	collector.Register(testSensor{
		id:      "gpu.dcgm",
		signals: collect.Signals{gpusignals.PowerDraw, gpusignals.TemperatureCelsius},
		measure: func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			return nil, errors.New("DCGM unavailable")
		},
	})

	batch, err := collector.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if batch.GetCollectionId() == "" {
		t.Fatal("Check() returned an empty collection ID")
	}
	if len(batch.GetObservations()) != 2 {
		t.Fatalf("Check() returned %d observations, want 2", len(batch.GetObservations()))
	}
	for _, current := range batch.GetObservations() {
		if current.GetSource() != "gpu.dcgm" {
			t.Errorf("observation source = %q, want %q", current.GetSource(), "gpu.dcgm")
		}
		failure := current.GetCollectionError()
		if failure == nil {
			t.Fatalf("signal %q has no collection error", current.GetSignalId())
		}
		if failure.GetSource() != "gpu.dcgm" {
			t.Errorf("collection error source = %q, want %q", failure.GetSource(), "gpu.dcgm")
		}
	}
}

func TestCheckRejectsObservationWithoutOutcome(t *testing.T) {
	collector := New()
	collector.Register(testSensor{
		id:      "gpu.dcgm",
		signals: collect.Signals{gpusignals.PowerDraw},
		measure: func(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
			return &observation.ObservationBatch{Observations: []*observation.Observation{
				{SignalId: gpusignals.PowerDraw.ID()},
			}}, nil
		},
	})

	batch, err := collector.Check(context.Background())
	if err == nil {
		t.Fatal("Check() returned nil error for an observation without an outcome")
	}
	if batch != nil {
		t.Fatalf("Check() batch = %v, want nil", batch)
	}
}

func TestServeRoutesGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveOnListener(listener, New())
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop after its listener closed")
		}
	})

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	batch, err := healthv1.NewCheckServiceClient(conn).Check(ctx, &healthv1.CheckRequest{})
	if err != nil {
		t.Fatalf("Check() over gRPC error = %v", err)
	}
	if batch.GetCollectionId() == "" {
		t.Fatal("Check() over gRPC returned an empty collection ID")
	}
}
