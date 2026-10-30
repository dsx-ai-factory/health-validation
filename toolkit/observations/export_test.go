// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

const testNode = "node-1"

// countMeasurement satisfies health.Measurement without being a struct.
type countMeasurement int

func (countMeasurement) Category() health.Category           { return health.GPU }
func (countMeasurement) Errors() []health.ErroredMeasurement { return nil }

func TestFromMeasurementsRejectsMeasurementsItCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		m    health.Measurement
		want string
	}{
		"nil interface": {nil, "is nil"},
		"nil pointer":   {(*gpu.GPUMeasurement)(nil), "is nil"},
		"non-struct":    {countMeasurement(3), "is not a struct"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), tc.m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}

	exported, err := FromMeasurements("gpu", testNode, time.Unix(1, 0), &gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil || len(exported) == 0 {
		t.Fatalf("a non-nil pointer must export: %v, %d observations", err, len(exported))
	}
}

func TestFromMeasurementsKeepsCatalogSignalNames(t *testing.T) {
	m := gpu.GPUMeasurement{GPUID: "GPU-0", AveragePowerDrawW: 412.5, GPUPstate: 2, HWThermalSlowdown: true}

	bySignal := exportBySignal(t, m)
	for signal, want := range map[string]*observation.Value{
		"gpu.gpu_id":                  stringValue("GPU-0"),
		"gpu.average_power_draw_w":    doubleValue(412.5),
		"gpu.gpu_pstate":              intValue(2),
		"gpu.hw_thermal_slowdown":     boolValue(true),
		"gpu.compute_process_count":   intValue(0),
		"gpu.hw_power_brake_slowdown": boolValue(false),
	} {
		got := bySignal[signal]
		if got == nil {
			t.Fatalf("missing %q in %v", signal, keys(bySignal))
		}
		if got.GetValue().String() != want.String() {
			t.Errorf("%s = %v, want %v", signal, got.GetValue(), want)
		}
		if got.GetTarget().GetGpu().GetUuid() != "GPU-0" || got.GetSource() != "toolkit.gpu" {
			t.Errorf("%s target/source = %v / %q", signal, got.GetTarget(), got.GetSource())
		}
	}
}

// Every json-tagged field of every built-in measurement is exported, so the
// batch carries every signal the catalog's checks can read.
func TestFromMeasurementsExportsEveryField(t *testing.T) {
	for _, m := range []health.Measurement{
		gpu.GPUMeasurement{GPUID: "GPU-0"},
		nvlink.NVLinkMeasurement{GPUID: "GPU-0"},
		cpu.CPUMeasurement{},
		node.NodeMeasurement{},
		xid.XidMeasurement{},
		infiniband.LinkFlapMeasurement{},
	} {
		t.Run(m.Category().String(), func(t *testing.T) {
			var want []string
			typ := reflect.TypeOf(m)
			for i := range typ.NumField() {
				if name, ok := signalName(typ.Field(i)); ok {
					want = append(want, m.Category().String()+"."+name)
				}
			}
			got := keys(exportBySignal(t, m))
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("exported %v, want %v", got, want)
			}
		})
	}
}

func TestFromMeasurementsTargetsNodeForHostWideMeasurements(t *testing.T) {
	observations, err := FromMeasurements("cpu", testNode, time.Unix(1, 0), cpu.CPUMeasurement{OnlineCPUCount: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range observations {
		if o.GetTarget().GetNode().GetId() != testNode {
			t.Fatalf("%s target = %v, want node %q", o.GetSignalId(), o.GetTarget(), testNode)
		}
	}

	untargeted, err := FromMeasurements("cpu", "", time.Unix(1, 0), cpu.CPUMeasurement{})
	if err != nil {
		t.Fatal(err)
	}
	if untargeted[0].GetTarget() != nil {
		t.Fatalf("want no target without a node ID, got %v", untargeted[0].GetTarget())
	}
}

func TestFromMeasurementsEncodesCollectionsAsJSON(t *testing.T) {
	bySignal := exportBySignal(t, xid.XidMeasurement{
		XidCounts: map[int64]int64{79: 2},
		Events:    []xid.XidEvent{{}},
	})

	var counts map[string]int64
	if err := json.Unmarshal([]byte(bySignal["xid.xid_counts"].GetValue().GetStringValue()), &counts); err != nil || counts["79"] != 2 {
		t.Fatalf("xid_counts = %q (%v)", bySignal["xid.xid_counts"].GetValue().GetStringValue(), err)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(bySignal["xid.events"].GetValue().GetStringValue()), &events); err != nil || len(events) != 1 {
		t.Fatalf("events = %q (%v)", bySignal["xid.events"].GetValue().GetStringValue(), err)
	}
	// An unset map or list is an empty collection, not JSON null.
	if got := bySignal["xid.fatal_xid_counts"].GetValue().GetStringValue(); got != "{}" {
		t.Fatalf("fatal_xid_counts = %q, want {}", got)
	}
	if got := exportBySignal(t, xid.XidMeasurement{})["xid.events"].GetValue().GetStringValue(); got != "[]" {
		t.Fatalf("events = %q, want []", got)
	}
}

func TestFromMeasurementsReportsErrorsAndUnsupported(t *testing.T) {
	m := nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18}
	m.AddError(fmt.Errorf("speed read: %w", health.ErrCollectionTimeout), nvlink.SignalSpeedMBytesPerSec)
	m.AddError(errors.New("p2p probe failed"), nvlink.SignalUnhealthyP2PPeerCount)
	m.MarkUnsupported(nvlink.SignalFabricRouteUnhealthy)

	bySignal := exportBySignal(t, m)
	if got := bySignal["nvlink.speed_mbytes_per_sec"].GetCollectionError().GetCategory(); got != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("speed category = %v", got)
	}
	if got := bySignal["nvlink.unhealthy_p2p_peer_count"].GetCollectionError().GetCategory(); got != observation.CollectionErrorCategorySourceError {
		t.Fatalf("p2p category = %v", got)
	}
	if got := bySignal["nvlink.fabric_route_unhealthy"].GetNoValue().GetReason(); got != observation.NoValueReasonNotSupported {
		t.Fatalf("route unhealthy reason = %v", got)
	}
	if got := bySignal["nvlink.link_count"].GetValue().GetIntValue(); got != 18 {
		t.Fatalf("link_count = %d", got)
	}
}

func TestFromMeasurementsWholeSensorFailureHasNoTarget(t *testing.T) {
	failed := gpu.ErroredGPU("gpu", errors.New("NVML init failed"), gpu.MeasurementSignals()...)

	for signal, o := range exportBySignal(t, failed) {
		if o.GetTarget() != nil || o.GetCollectionError() == nil {
			t.Fatalf("%s: want a target-less collection error, got %v", signal, o)
		}
	}
}

func exportBySignal(t *testing.T, m health.Measurement) map[string]*observation.Observation {
	t.Helper()
	observations, err := FromMeasurements(m.Category().String(), testNode, time.Unix(1, 0), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := observation.ValidateBatch(&observation.ObservationBatch{Observations: observations}); err != nil {
		t.Fatal(err)
	}
	bySignal := make(map[string]*observation.Observation, len(observations))
	for _, o := range observations {
		if _, dup := bySignal[o.GetSignalId()]; dup {
			t.Fatalf("signal %q exported twice", o.GetSignalId())
		}
		bySignal[o.GetSignalId()] = o
	}
	return bySignal
}

func keys(bySignal map[string]*observation.Observation) []string {
	out := make([]string, 0, len(bySignal))
	for k := range bySignal {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// targetLabel renders a target as kind:id for comparison.
func targetLabel(target *observation.ObservationTarget) string {
	switch {
	case target == nil:
		return "none"
	case target.GetGpu() != nil:
		return "gpu:" + target.GetGpu().GetUuid()
	case target.GetPci() != nil:
		return "pci:" + target.GetPci().GetAddress()
	case target.GetNode() != nil:
		return "node:" + target.GetNode().GetId()
	}
	return fmt.Sprintf("other:%v", target)
}

func TestFromMeasurementsTargetsXidByIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		nodeID string
		want   string
		m      xid.XidMeasurement
	}{
		"UUID":                {testNode, "gpu:GPU-a", xid.XidMeasurement{GPUID: "GPU-a", PCIAddress: "0000:89:00"}},
		"PCI address only":    {testNode, "pci:0000:c1:00.0", xid.XidMeasurement{PCIAddress: "0000:c1:00"}},
		"no identity":         {testNode, "node:" + testNode, xid.XidMeasurement{Since: "boot"}},
		"failed scan":         {testNode, "node:" + testNode, xid.ErroredXid("boot", errors.New("journal unreadable"))},
		"unparseable address": {testNode, "node:" + testNode, xid.XidMeasurement{PCIAddress: "0000:01:20"}},
		"no node ID":          {"", "none", xid.XidMeasurement{Since: "boot"}},
	} {
		t.Run(name, func(t *testing.T) {
			observations, err := FromMeasurements("xid", tc.nodeID, time.Unix(1, 0), tc.m)
			if err != nil {
				t.Fatal(err)
			}
			if err := observation.ValidateBatch(&observation.ObservationBatch{Observations: observations}); err != nil {
				t.Fatal(err)
			}
			for _, o := range observations {
				if got := targetLabel(o.GetTarget()); got != tc.want {
					t.Fatalf("%s target = %s, want %s", o.GetSignalId(), got, tc.want)
				}
			}
		})
	}
}
