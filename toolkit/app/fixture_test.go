// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

// cpuAppSensor stands in for the sysfs CPU sensor under a WithCollect claim,
// reporting fixed counts on the node. calls, when set, counts measurements.
type cpuAppSensor struct {
	calls    *int
	possible int64
	online   int64
	numa     int64
}

func (cpuAppSensor) ID() string               { return "test.cpu" }
func (cpuAppSensor) Signals() collect.Signals { return cpusignals.All() }

func (s cpuAppSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if s.calls != nil {
		*s.calls++
	}
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	values := map[string]int64{
		cpusignals.PossibleCount.ID():       s.possible,
		cpusignals.OnlineCount.ID():         s.online,
		cpusignals.NUMANodeOnlineCount.ID(): s.numa,
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	for _, signal := range selected {
		value, ok := values[signal.ID()]
		if !ok {
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId: signal.ID(), Source: s.ID(), Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
			Unit: signal.Unit(), Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: value}}},
		})
	}
	return batch, nil
}

// healthyCPU matches the built-in CPU checks' GB200 defaults.
func healthyCPU() cpuAppSensor { return cpuAppSensor{possible: 1, online: 1, numa: 34} }

// withCPU replaces the built-in sysfs CPU source with s.
func withCPU(a *App, s cpuAppSensor) *App {
	return a.WithCollect(s, observations.MappedSignalsFor(health.CPU)...)
}

// withCleanXid replaces the built-in journal Xid source with a clean scan.
func withCleanXid(a *App) *App {
	calls := 0
	return a.WithCollect(xidAppSensor{calls: &calls, obs: []*observation.Observation{xidAppMarker()}}, xidsignals.All().IDs()...)
}

// withCleanLinkFlap replaces the built-in journal link-flap source with a
// clean scan.
func withCleanLinkFlap(a *App) *App {
	calls := 0
	return a.WithCollect(linkFlapAppSensor{calls: &calls, obs: []*observation.Observation{linkFlapAppMarker()}}, ibsignals.LinkFlapSignals().IDs()...)
}

// baseTestApp runs off the host on fake GPUs and inventory.
func baseTestApp() *App {
	a := New().WithProgramName("test")
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	return a
}

// testApp runs off the host: fake GPUs and inventory, a healthy CPU, and a
// clean link-flap scan. Xid still reads the journal; see cadenceRunApp.
func testApp() *App { return testAppWithCPU(healthyCPU()) }

func testAppWithCPU(cpu cpuAppSensor) *App {
	return withCleanLinkFlap(withCPU(baseTestApp(), cpu))
}

// builtinLinkFlapApp is cadenceRunApp with the built-in journal link-flap
// source.
func builtinLinkFlapApp() *App { return withCleanXid(withCPU(baseTestApp(), healthyCPU())) }

// cadenceRunApp is testApp with a clean Xid scan, so a run touches no host
// source.
func cadenceRunApp() *App { return withCleanXid(testApp()) }

// measurementSensor is a toolkit sensor reporting fixed measurements, or
// failing with err and reporting errored instead.
type measurementSensor struct {
	err     error
	errored func(error) []health.Measurement
	name    string
	ms      []health.Measurement
}

func (s measurementSensor) Name() string { return s.name }
func (s measurementSensor) Measure(context.Context) ([]health.Measurement, error) {
	return s.ms, s.err
}

func (s measurementSensor) Errored(err error) []health.Measurement {
	if s.errored == nil {
		return nil
	}
	return s.errored(err)
}
