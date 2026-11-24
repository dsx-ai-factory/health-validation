// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"slices"
	"testing/fstest"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisensor "github.com/dsx-ai-factory/health-validation/collect/sensors/pci"
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

// baseTestApp runs off the host on fake GPUs and inventory, with one healthy
// InfiniBand port claimed so a run never reads the host's /sys/class/infiniband.
func baseTestApp() *App {
	a := New().WithProgramName("test")
	a.host = fakeHost{b: spyBackend{called: new(bool)}}
	return withPCI(healthyInfiniBand(a), spyGPUs, 64)
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

// healthyInfiniBand replaces the stock sysfs InfiniBand source with one
// reporting a single healthy port. Reporting no ports would fail the port
// check, as it does on a host whose RDMA driver is missing.
func healthyInfiniBand(a *App) *App {
	return a.WithCollect(healthyIBSensor{}, healthyIBSensor{}.Signals().IDs()...)
}

type healthyIBSensor struct{}

func (healthyIBSensor) ID() string { return "test.infiniband" }

// Signals are the port signals the InfiniBand adapter maps.
func (healthyIBSensor) Signals() collect.Signals {
	mapped := observations.MappedSignalsFor(health.InfiniBand)
	return slices.DeleteFunc(ibsignals.PortSignals(), func(s collect.Signal) bool { return !slices.Contains(mapped, s.ID()) })
}

func (s healthyIBSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	values := map[string]*observation.Value{
		ibsignals.StateName.ID():         {Kind: &observation.Value_StringValue{StringValue: "ACTIVE"}},
		ibsignals.PhysicalStateName.ID(): {Kind: &observation.Value_StringValue{StringValue: "LinkUp"}},
		ibsignals.RateGbps.ID():          {Kind: &observation.Value_DoubleValue{DoubleValue: 400}},
		ibsignals.LinkLayer.ID():         {Kind: &observation.Value_StringValue{StringValue: "InfiniBand"}},
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	now := timestamppb.New(op.Cycle().StartedAt())
	for _, signal := range selected {
		value, ok := values[signal.ID()]
		if !ok {
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   signal.ID(),
			Unit:       signal.Unit(),
			Source:     s.ID(),
			Target:     observation.InfiniBandPortTarget("mlx5_0", 1),
			ObservedAt: now,
			Outcome:    &observation.Observation_Value{Value: value},
		})
	}
	return batch, nil
}

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

// withPCI supplies link properties for the synthetic inventory's GPUs.
func withPCI(a *App, gpus, maxSpeedGTs int) *App {
	files := make(fstest.MapFS)
	for i := range gpus {
		base := fmt.Sprintf("sys/bus/pci/devices/0000:%02x:00.0/", i+1)
		for field, value := range map[string]string{
			"class":          "0x030200\n",
			"vendor":         "0x10de\n",
			"max_link_width": "16\n",
			"max_link_speed": fmt.Sprintf("%d.0 GT/s PCIe\n", maxSpeedGTs),
		} {
			files[base+field] = &fstest.MapFile{Data: []byte(value)}
		}
	}
	return a.WithCollect(pcisensor.NewGPUs(files), "pci.link.width.max", "pci.link.generation.max")
}
