// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func TestToMeasurementsCountsInactiveLinks(t *testing.T) {
	state := gpusignals.NVLinkState
	tests := []struct {
		name    string
		obs     []*observation.Observation
		want    int64
		failed  bool
		timeout bool
	}{
		{name: "one disabled link", want: 1, obs: []*observation.Observation{
			linkValue(state, "GPU-0", 0, 0), linkValue(state, "GPU-0", 1, 1), linkValue(state, "GPU-0", 2, 1),
		}},
		{name: "index past the last link", want: 0, obs: []*observation.Observation{
			linkValue(state, "GPU-0", 0, 1), linkFailure(state, observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_INVALID_ARGUMENT", observation.CollectionErrorCategorySourceError),
		}},
		{name: "no NVLink", want: 0, obs: []*observation.Observation{
			linkFailure(state, observation.GPUTarget("GPU-0"), "ERROR_NOT_SUPPORTED", observation.CollectionErrorCategoryUnsupported),
		}},
		{name: "failed link read", failed: true, obs: []*observation.Observation{
			linkValue(state, "GPU-0", 0, 0), linkFailure(state, observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError),
		}},
		{name: "lost GPU", failed: true, obs: []*observation.Observation{
			linkFailure(state, observation.GPUTarget("GPU-0"), "ERROR_GPU_IS_LOST", observation.CollectionErrorCategoryUnavailable),
		}},
		{name: "link read timeout", failed: true, timeout: true, obs: []*observation.Observation{
			linkFailure(state, observation.NVLinkEndpointTarget("GPU-0", 0), "", observation.CollectionErrorCategoryTimeout),
		}},
		{name: "unsupported link above 0 ends the links", want: 1, obs: []*observation.Observation{
			linkValue(state, "GPU-0", 0, 0), linkFailure(state, observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_NOT_SUPPORTED", observation.CollectionErrorCategoryUnsupported),
		}},
		{name: "repeated link", failed: true, obs: []*observation.Observation{
			linkValue(state, "GPU-0", 0, 0), linkValue(state, "GPU-0", 0, 0),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := only[nvlink.NVLinkMeasurement](t, ToMeasurements(test.obs))
			failure := errorFor(m, nvlink.SignalInactiveCount)
			if (failure != nil) != test.failed {
				t.Fatalf("inactive_count failure = %v, want failed=%t", failure, test.failed)
			}
			if failure != nil && errors.Is(failure, health.ErrCollectionTimeout) != test.timeout {
				t.Fatalf("timeout = %t, want %t", !test.timeout, test.timeout)
			}
			if !test.failed && m.InactiveCount != test.want {
				t.Fatalf("inactive_count = %d, want %d", m.InactiveCount, test.want)
			}
		})
	}
}

func TestToMeasurementsKeepsEachGPUsLinksApart(t *testing.T) {
	state := gpusignals.NVLinkState
	measurements := ToMeasurements([]*observation.Observation{
		linkValue(state, "GPU-0", 0, 0), linkValue(state, "GPU-1", 0, 1), linkValue(state, "GPU-0", 1, 0),
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess)),
	})
	byGPU := map[string]nvlink.NVLinkMeasurement{}
	for _, m := range measurements {
		v := m.(nvlink.NVLinkMeasurement)
		byGPU[v.GPUID] = v
	}
	if byGPU["GPU-0"].InactiveCount != 2 || byGPU["GPU-1"].InactiveCount != 0 {
		t.Fatalf("measurements = %+v", byGPU)
	}
	if errorFor(byGPU["GPU-1"], nvlink.SignalInactiveCount) != nil {
		t.Fatal("GPU-1 inactive_count failed")
	}
}

// A source-wide failure fails the field through the mapping table, and a GPU
// with no link observations leaves it unfed; neither can pass.
func TestToMeasurementsInactiveLinksNeverPassUncollected(t *testing.T) {
	sourceTimeout := failedObservation(gpusignals.NVLinkState.ID(), &observation.CollectionError{Source: "nvml", Operation: "Measure", Category: observation.CollectionErrorCategoryTimeout})
	sourceTimeout.Target = nil
	m := only[nvlink.NVLinkMeasurement](t, ToMeasurements([]*observation.Observation{sourceTimeout}))
	if failure := errorFor(m, nvlink.SignalInactiveCount); failure == nil || !errors.Is(failure, health.ErrCollectionTimeout) {
		t.Fatalf("source timeout: inactive_count failure = %v", failure)
	}

	// Common and per-link speed both feed speed_mbytes_per_sec; their
	// source timeouts stay one timeout, not a repeated signal.
	var speedTimeouts []*observation.Observation
	for _, s := range []collect.Signal{gpusignals.NVLinkSpeedCommon, gpusignals.NVLinkEndpointSpeed} {
		o := failedObservation(s.ID(), &observation.CollectionError{Source: "nvml", Operation: "Measure", Category: observation.CollectionErrorCategoryTimeout})
		o.Target = nil
		speedTimeouts = append(speedTimeouts, o)
	}
	m = only[nvlink.NVLinkMeasurement](t, ToMeasurements(speedTimeouts))
	for _, entry := range m.Errors() {
		if slices.Contains(entry.FailedSignals(), nvlink.SignalSpeedMBytesPerSec) && !errors.Is(entry.Err(), health.ErrCollectionTimeout) {
			t.Fatalf("speed failure %v is not a timeout", entry.Err())
		}
	}
	if failure := errorFor(m, nvlink.SignalSpeedMBytesPerSec); failure == nil {
		t.Fatal("speed did not fail on source timeouts")
	}

	m = only[nvlink.NVLinkMeasurement](t, ToMeasurements([]*observation.Observation{
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess)),
	}))
	if !slices.Contains(missingSignals(m), nvlink.SignalInactiveCount) {
		t.Fatalf("missing = %v, want inactive_count", missingSignals(m))
	}
}

func TestToMeasurementsMapsLinkCountAndSpeed(t *testing.T) {
	count, common, perLink := gpusignals.NVLinkLinkCount, gpusignals.NVLinkSpeedCommon, gpusignals.NVLinkEndpointSpeed
	unsupported := linkFailure(common, observation.GPUTarget("GPU-0"), "ERROR_NOT_SUPPORTED", observation.CollectionErrorCategoryUnsupported)
	tests := []struct {
		name        string
		obs         []*observation.Observation
		count       int64
		speed       int64
		countFailed bool
		speedFailed bool
	}{
		{name: "common speed", count: 18, speed: 53125, obs: []*observation.Observation{gpuUint(count, 18), gpuUint(common, 53125)}},
		{name: "zero common speed passes through", count: 18, speed: 0, obs: []*observation.Observation{gpuUint(count, 18), gpuUint(common, 0)}},
		{name: "slowest link when common speed is unsupported", count: 2, speed: 20000, obs: []*observation.Observation{
			gpuUint(count, 2), unsupported, linkValue(perLink, "GPU-0", 0, 25000), linkValue(perLink, "GPU-0", 1, 20000), linkValue(perLink, "GPU-0", 2, 1),
		}},
		{name: "fallback missing a link", count: 2, speedFailed: true, obs: []*observation.Observation{
			gpuUint(count, 2), unsupported, linkValue(perLink, "GPU-0", 0, 25000),
		}},
		{name: "fallback zero speed", count: 1, speedFailed: true, obs: []*observation.Observation{
			gpuUint(count, 1), unsupported, linkValue(perLink, "GPU-0", 0, 0),
		}},
		{name: "fallback failed link", count: 1, speedFailed: true, obs: []*observation.Observation{
			gpuUint(count, 1), unsupported, linkFailure(perLink, observation.NVLinkEndpointTarget("GPU-0", 0), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError),
		}},
		{name: "fallback without a link count", countFailed: true, speedFailed: true, obs: []*observation.Observation{
			linkFailure(count, observation.GPUTarget("GPU-0"), "ERROR_NOT_SUPPORTED", observation.CollectionErrorCategoryUnsupported), unsupported, linkValue(perLink, "GPU-0", 0, 25000),
		}},
		{name: "common speed error", count: 18, speedFailed: true, obs: []*observation.Observation{
			gpuUint(count, 18), linkFailure(common, observation.GPUTarget("GPU-0"), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError),
			linkValue(perLink, "GPU-0", 0, 25000),
		}},
		{name: "count error", speed: 53125, countFailed: true, obs: []*observation.Observation{
			linkFailure(count, observation.GPUTarget("GPU-0"), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError), gpuUint(common, 53125),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := only[nvlink.NVLinkMeasurement](t, ToMeasurements(test.obs))
			if failed := errorFor(m, nvlink.SignalLinkCount) != nil; failed != test.countFailed || (!failed && m.LinkCount != test.count) {
				t.Errorf("link_count = %d, failure %v", m.LinkCount, errorFor(m, nvlink.SignalLinkCount))
			}
			if failed := errorFor(m, nvlink.SignalSpeedMBytesPerSec) != nil; failed != test.speedFailed || (!failed && m.SpeedMBytesPerSec != test.speed) {
				t.Errorf("speed = %d, failure %v", m.SpeedMBytesPerSec, errorFor(m, nvlink.SignalSpeedMBytesPerSec))
			}
		})
	}
}

func TestToMeasurementsANDsLinkCapabilities(t *testing.T) {
	signals := gpusignals.EndpointCapabilitySignals()
	fields := []struct {
		get    func(nvlink.NVLinkMeasurement) bool
		signal string
	}{
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportP2P }, nvlink.SignalAllLinksSupportP2P},
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportSysmemAccess }, nvlink.SignalAllLinksSupportSysmemAccess},
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportP2PAtomics }, nvlink.SignalAllLinksSupportP2PAtomics},
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportSysmemAtomics }, nvlink.SignalAllLinksSupportSysmemAtomics},
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportSLI }, nvlink.SignalAllLinksSupportSLI},
		{func(m nvlink.NVLinkMeasurement) bool { return m.AllLinksSupportLink }, nvlink.SignalAllLinksSupportLink},
	}
	// link returns all six capability results for one link; zero lists the
	// capabilities the link does not support.
	link := func(index uint32, zero ...int) []*observation.Observation {
		var obs []*observation.Observation
		for i, s := range signals {
			value := uint64(1)
			if slices.Contains(zero, i) {
				value = 0
			}
			obs = append(obs, linkValue(s, "GPU-0", index, value))
		}
		return obs
	}
	// stateFailed is how the sensor reports a failed state read on every
	// capability of that link.
	stateFailed := func(target *observation.ObservationTarget, sourceError string) []*observation.Observation {
		var obs []*observation.Observation
		for _, s := range signals {
			obs = append(obs, linkFailure(s, target, sourceError, observation.CollectionErrorCategorySourceError))
		}
		return obs
	}
	const p2p, sli = 0, 4
	capabilityFailed := linkFailure(signals[p2p], observation.NVLinkEndpointTarget("GPU-0", 0), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError)
	capabilityFailed.GetCollectionError().Operation = "GetNvLinkCapability"
	capabilityInvalid := linkFailure(signals[p2p], observation.NVLinkEndpointTarget("GPU-0", 0), "ERROR_INVALID_ARGUMENT", observation.CollectionErrorCategorySourceError)
	capabilityInvalid.GetCollectionError().Operation = "GetNvLinkCapability"
	tests := []struct {
		name        string
		obs         []*observation.Observation
		unsupported []int
		failed      []int
	}{
		{name: "every link supports everything", obs: slices.Concat(link(0), link(1))},
		{name: "one link without SLI", obs: slices.Concat(link(0), link(1, sli)), unsupported: []int{sli}},
		{name: "failed capability read", obs: slices.Concat([]*observation.Observation{capabilityFailed}, link(0)[1:], link(1)), failed: []int{p2p}},
		{name: "link past the last", obs: slices.Concat(link(0), stateFailed(observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_INVALID_ARGUMENT"))},
		{name: "no present links", obs: stateFailed(observation.NVLinkEndpointTarget("GPU-0", 0), "ERROR_INVALID_ARGUMENT")},
		{
			name: "state failure keeps resolved results", unsupported: []int{sli}, failed: []int{0, 1, 2, 3, 5},
			obs: slices.Concat(link(0, sli), stateFailed(observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_UNKNOWN")),
		},
		{name: "no NVLink", obs: stateFailed(observation.GPUTarget("GPU-0"), "ERROR_NOT_SUPPORTED"), failed: []int{0, 1, 2, 3, 4, 5}},
		// Only a state read skips INVALID_ARGUMENT; a capability read failing
		// with it is a failure.
		{name: "invalid capability read", obs: slices.Concat([]*observation.Observation{capabilityInvalid}, link(0)[1:]), failed: []int{p2p}},
		{name: "unsupported link above 0", obs: slices.Concat(link(0), stateFailed(observation.NVLinkEndpointTarget("GPU-0", 1), "ERROR_NOT_SUPPORTED")), failed: []int{0, 1, 2, 3, 4, 5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := only[nvlink.NVLinkMeasurement](t, ToMeasurements(test.obs))
			for i, field := range fields {
				failure := errorFor(m, field.signal)
				if (failure != nil) != slices.Contains(test.failed, i) {
					t.Errorf("%s failure = %v", field.signal, failure)
					continue
				}
				if failure == nil && field.get(m) == slices.Contains(test.unsupported, i) {
					t.Errorf("%s = %t", field.signal, field.get(m))
				}
			}
		})
	}
}

func TestToMeasurementsCountsUnhealthyP2PPeers(t *testing.T) {
	status := gpusignals.NVLinkP2PStatus
	pairFailure := linkFailure(status, observation.GPUPairTarget("GPU-0", "GPU-2"), "ERROR_UNKNOWN", observation.CollectionErrorCategorySourceError)
	sourceFailure := failedObservation(status.ID(), &observation.CollectionError{Source: "nvml", Operation: "Measure", Category: observation.CollectionErrorCategoryUnavailable})
	sourceFailure.Target = nil
	state := linkValue(gpusignals.NVLinkState, "GPU-0", 0, 1)
	noPeers := observation.NewNoValueObservation(status.ID(), status.Unit(), "nvml", observation.GPUTarget("GPU-0"), timestamppb.Now(),
		&observation.NoValue{Reason: observation.NoValueReasonNoTargets})
	tests := []struct {
		name   string
		obs    []*observation.Observation
		want   int64
		failed bool
	}{
		{name: "one peer not OK", want: 1, obs: []*observation.Observation{
			pairValue("GPU-0", "GPU-1", 0), pairValue("GPU-0", "GPU-2", 5), pairValue("GPU-1", "GPU-0", 5), state,
		}},
		{name: "failed pair", failed: true, obs: []*observation.Observation{pairValue("GPU-0", "GPU-1", 0), pairFailure}},
		{name: "no peers", want: 0, obs: []*observation.Observation{state, noPeers}},
		{name: "P2P not collected", failed: true, obs: []*observation.Observation{state}},
		{name: "source failure", failed: true, obs: []*observation.Observation{state, sourceFailure}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var m *nvlink.NVLinkMeasurement
			for _, current := range ToMeasurements(test.obs) {
				if v := current.(nvlink.NVLinkMeasurement); v.GPUID == "GPU-0" {
					m = &v
				}
			}
			if m == nil {
				t.Fatal("no GPU-0 measurement")
			}
			failure := errorFor(*m, nvlink.SignalUnhealthyP2PPeerCount)
			if (failure != nil) != test.failed || (failure == nil && m.UnhealthyP2PPeerCount != test.want) {
				t.Fatalf("unhealthy_p2p_peer_count = %d, failure %v", m.UnhealthyP2PPeerCount, failure)
			}
		})
	}
}

func pairValue(source, peer string, status uint64) *observation.Observation {
	o := linkValue(gpusignals.NVLinkP2PStatus, source, 0, status)
	o.Target = observation.GPUPairTarget(source, peer)
	return o
}

func gpuUint(s collect.Signal, value uint64) *observation.Observation {
	o := linkValue(s, "GPU-0", 0, value)
	o.Target = observation.GPUTarget("GPU-0")
	return o
}

func linkValue(s collect.Signal, gpuUUID string, link uint32, value uint64) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "nvml", Target: observation.NVLinkEndpointTarget(gpuUUID, link), ObservedAt: timestamppb.Now(), Unit: s.Unit(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}},
	}
}

// linkFailure is a failed read as the NVML sensor records it, with the
// operation it uses for s.
func linkFailure(s collect.Signal, target *observation.ObservationTarget, sourceError string, category observation.CollectionErrorCategory) *observation.Observation {
	operation := "GetNvLinkState"
	switch s.ID() {
	case gpusignals.NVLinkLinkCount.ID(), gpusignals.NVLinkSpeedCommon.ID(), gpusignals.NVLinkEndpointSpeed.ID():
		operation = "GetFieldValues"
	case gpusignals.NVLinkP2PStatus.ID():
		operation = "GetP2PStatus"
	}
	return observation.NewCollectionErrorObservation(s.ID(), s.Unit(), "nvml", operation, target, timestamppb.Now(),
		&observation.CollectionError{Source: "nvml", Operation: operation, Category: category, SourceError: sourceError})
}

// errorFor returns the error m reports for signal, or nil.
func errorFor(m health.Measurement, signal string) error {
	for _, entry := range m.Errors() {
		if slices.Contains(entry.FailedSignals(), signal) {
			return entry.Err()
		}
	}
	return nil
}
