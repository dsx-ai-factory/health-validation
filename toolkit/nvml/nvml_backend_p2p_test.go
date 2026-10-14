// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// deviceWithP2PStatus returns a mock device whose GetP2PStatus calls are
// answered by getStatus (the peer argument is ignored by these tests).
func deviceWithP2PStatus(getStatus func() (gonvml.GpuP2PStatus, gonvml.Return)) *mock.Device {
	return &mock.Device{
		GetP2PStatusFunc: func(gonvml.Device, gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
			return getStatus()
		},
	}
}

// p2pContext builds a ProbeContext measuring self against peers.
func p2pContext(self gonvml.Device, peers ...gonvml.Device) ProbeContext {
	pc := ProbeContext{Self: ProbeDevice{Device: self, UUID: "GPU-SELF", Index: 0}}
	for i, d := range peers {
		pc.Peers = append(pc.Peers, ProbeDevice{Device: d, Index: i + 1})
	}
	return pc
}

func TestMeasureDeviceP2P_AllReachable(t *testing.T) {
	d0 := deviceWithP2PStatus(func() (gonvml.GpuP2PStatus, gonvml.Return) {
		return gonvml.P2P_STATUS_OK, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceP2P(p2pContext(d0, &mock.Device{}, &mock.Device{}), &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if m.UnhealthyP2PPeerCount != 0 {
		t.Errorf("UnhealthyP2PPeerCount = %d, want 0", m.UnhealthyP2PPeerCount)
	}
}

func TestMeasureDeviceP2P_CountsUnreachablePeers(t *testing.T) {
	calls := 0
	d0 := deviceWithP2PStatus(func() (gonvml.GpuP2PStatus, gonvml.Return) {
		calls++
		if calls == 1 {
			return gonvml.P2P_STATUS_OK, gonvml.SUCCESS
		}
		return gonvml.P2P_STATUS_NOT_SUPPORTED, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceP2P(p2pContext(d0, &mock.Device{}, &mock.Device{}), &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if m.UnhealthyP2PPeerCount != 1 {
		t.Errorf("UnhealthyP2PPeerCount = %d, want 1 (second peer not OK)", m.UnhealthyP2PPeerCount)
	}
	if calls != 2 {
		t.Errorf("GetP2PStatus calls = %d, want 2 (one per peer)", calls)
	}
}

func TestMeasureDeviceP2P_QueryFailureFlagsSignal(t *testing.T) {
	calls := 0
	d0 := deviceWithP2PStatus(func() (gonvml.GpuP2PStatus, gonvml.Return) {
		calls++
		return gonvml.P2P_STATUS_UNKNOWN, gonvml.ERROR_TIMEOUT
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceP2P(p2pContext(d0, &mock.Device{}, &mock.Device{}), &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry (probe must stop after first failure)", errs)
	}
	if !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalUnhealthyP2PPeerCount}) {
		t.Errorf("FailedSignals = %v, want only %q", errs[0].FailedSignals(), nvlink.SignalUnhealthyP2PPeerCount)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok {
		t.Fatalf("Err = %T, want *Error", errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetP2PStatus" || nvmlErr.Return != gonvml.ERROR_TIMEOUT {
		t.Errorf("Err = %+v, want Op=DeviceGetP2PStatus Return=ERROR_TIMEOUT", nvmlErr)
	}
	if calls != 1 {
		t.Errorf("GetP2PStatus calls = %d, want 1 (probe must stop on first failure)", calls)
	}
}

// peersCaptureProbe records the ProbeContext of every Measure call so tests
// can assert on the peer lists the backend constructs.
type peersCaptureProbe struct {
	contexts []ProbeContext
}

func (p *peersCaptureProbe) Measure(pc ProbeContext) health.Measurement {
	p.contexts = append(p.contexts, pc)
	return nvlink.NVLinkMeasurement{GPUID: pc.Self.UUID}
}

func (p *peersCaptureProbe) Errored(id string, err error) health.Measurement {
	return nvlink.ErroredNVLink(id, err, nvlink.MeasurementSignals...)
}

// A slot whose handle never loaded is excluded from every peer list, so a
// pairwise read can never query it; the slot itself still fails via Errored.
func TestProbe_PeersExcludeSelfAndErroredSlots(t *testing.T) {
	devA := &mock.Device{GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-A", gonvml.SUCCESS }}
	devB := &mock.Device{GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-B", gonvml.SUCCESS }}
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 3, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(n int) (gonvml.Device, gonvml.Return) {
			switch n {
			case 0:
				return devA, gonvml.SUCCESS
			case 1:
				return nil, gonvml.ERROR_GPU_IS_LOST
			default:
				return devB, gonvml.SUCCESS
			}
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	p := &peersCaptureProbe{}
	ms, err := b.WithNVLinkProbe(p).MeasureNVLink()
	if err != nil {
		t.Fatalf("MeasureNVLink: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d measurements, want 3 (one per slot)", len(ms))
	}
	if len(p.contexts) != 2 {
		t.Fatalf("Measure calls = %d, want 2 (usable slots only)", len(p.contexts))
	}

	want := []struct {
		selfUUID  string
		peerUUID  string
		selfIndex int
		peerIndex int
	}{
		{selfUUID: "GPU-A", peerUUID: "GPU-B", selfIndex: 0, peerIndex: 2},
		{selfUUID: "GPU-B", peerUUID: "GPU-A", selfIndex: 2, peerIndex: 0},
	}
	for i, w := range want {
		pc := p.contexts[i]
		if pc.Self.UUID != w.selfUUID || pc.Self.Index != w.selfIndex {
			t.Errorf("contexts[%d].Self = {%q %d}, want {%q %d}", i, pc.Self.UUID, pc.Self.Index, w.selfUUID, w.selfIndex)
		}
		if len(pc.Peers) != 1 {
			t.Fatalf("contexts[%d].Peers has %d entries, want 1 (self and errored slot excluded)", i, len(pc.Peers))
		}
		if pc.Peers[0].UUID != w.peerUUID || pc.Peers[0].Index != w.peerIndex {
			t.Errorf("contexts[%d].Peers[0] = {%q %d}, want {%q %d}", i, pc.Peers[0].UUID, pc.Peers[0].Index, w.peerUUID, w.peerIndex)
		}
	}
}
