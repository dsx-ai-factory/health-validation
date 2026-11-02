// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
)

// parityGPU is what NVML reports for one GPU. The legacy backend reads it
// through a go-nvml mock; the collect path reads it through the collect NVML
// sensor, plus a DCGM sensor that derives the fabric fields from the same
// fabric info.
type parityGPU struct {
	inactive  map[int]bool
	linkSpeed map[int]uint32
	capOff    map[int]gonvml.NvLinkCapability // link -> capability it lacks
	stateRet  map[int]gonvml.Return
	p2p       map[string]gonvml.GpuP2PStatus
	p2pRet    map[string]gonvml.Return
	uuid      string
	links     int // the reported link count
	answering int // links whose reads succeed; 0 means links
	fabric    gonvml.GpuFabricInfo_v3
	speed     uint32
	countRet  gonvml.Return
	speedRet  gonvml.Return
	pastEnd   gonvml.Return
	fabricRet gonvml.Return
}

func (g *parityGPU) present() int {
	if g.answering > 0 {
		return g.answering
	}
	return g.links
}

func (g *parityGPU) state(link int) (gonvml.EnableState, gonvml.Return) {
	if ret, ok := g.stateRet[link]; ok {
		return gonvml.FEATURE_DISABLED, ret
	}
	switch {
	case link >= g.present():
		return gonvml.FEATURE_DISABLED, g.pastEnd
	case g.inactive[link]:
		return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
	default:
		return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
	}
}

func (g *parityGPU) capability(link int, c gonvml.NvLinkCapability) (uint32, gonvml.Return) {
	if link >= g.present() {
		return 0, gonvml.ERROR_INVALID_ARGUMENT
	}
	if off, ok := g.capOff[link]; ok && off == c {
		return 0, gonvml.SUCCESS
	}
	return 1, gonvml.SUCCESS
}

func (g *parityGPU) fields(values []gonvml.FieldValue) gonvml.Return {
	for i := range values {
		value, ret := uint32(0), gonvml.ERROR_NOT_SUPPORTED
		switch values[i].FieldId {
		case gonvml.FI_DEV_NVLINK_LINK_COUNT:
			value, ret = uint32(g.links), g.countRet
		case gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON:
			value, ret = g.speed, g.speedRet
		case gonvml.FI_DEV_NVLINK_GET_SPEED:
			if link := int(values[i].ScopeId); link < g.present() {
				value, ret = g.speed, gonvml.SUCCESS
				if speed, ok := g.linkSpeed[link]; ok {
					value = speed
				}
			}
		}
		values[i].NvmlReturn = uint32(ret)
		values[i].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
		binary.LittleEndian.PutUint32(values[i].Value[:4], value)
	}
	return gonvml.SUCCESS
}

// collectGPU is the collect sensor's view of a parityGPU.
type collectGPU struct {
	device.Device
	gpu *parityGPU
}

func (d *collectGPU) UUID() string { return d.gpu.uuid }

func (d *collectGPU) GetFieldValues(values []gonvml.FieldValue) gonvml.Return {
	return d.gpu.fields(values)
}

func (d *collectGPU) GetNvLinkState(link int) (gonvml.EnableState, gonvml.Return) {
	return d.gpu.state(link)
}

func (d *collectGPU) GetNvLinkCapability(link int, c gonvml.NvLinkCapability) (uint32, gonvml.Return) {
	return d.gpu.capability(link, c)
}

func (d *collectGPU) GetP2PStatus(peer gonvml.Device, index gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
	return d.gpu.p2pStatus(peer.(*collectGPU).gpu, index)
}

// p2pStatus answers only the NVLink P2P question both paths must ask.
func (g *parityGPU) p2pStatus(peer *parityGPU, index gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
	if index != gonvml.P2P_CAPS_INDEX_NVLINK {
		return 0, gonvml.ERROR_INVALID_ARGUMENT
	}
	return g.p2p[peer.uuid], g.p2pRet[peer.uuid]
}

// parityInstance is a collect NVML instance over the fixture GPUs and failed
// enumeration slots.
type parityInstance struct {
	block   chan struct{}
	devices map[string]device.Device
	records []nvmlsource.InventoryDevice
}

func (*parityInstance) NVMLExists() bool                                 { return true }
func (i *parityInstance) Devices() map[string]device.Device              { return i.devices }
func (*parityInstance) Refresh() error                                   { return nil }
func (*parityInstance) Shutdown() error                                  { return nil }
func (*parityInstance) InitError() error                                 { return nil }
func (i *parityInstance) InventoryDevices() []nvmlsource.InventoryDevice { return i.records }

func (i *parityInstance) Read(read func(map[string]device.Device) error) error {
	if i.block != nil {
		<-i.block
	}
	return read(i.devices)
}

var fabricSignals = collect.Signals{
	gpusignals.FabricManagerStatus,
	gpusignals.NVLinkFabricBandwidthDegraded,
	gpusignals.NVLinkFabricRouteRecoveryActive,
	gpusignals.NVLinkFabricRouteUnhealthy,
	gpusignals.NVLinkFabricAccessTimeoutRecovery,
}

// parityDCGM stands in for the DCGM sensor: field 170 is the Fabric Manager
// status DCGM derives from the fabric info's state and status, field 174 its
// health mask, a fabric call NVML reports unsupported is a NoValue, another
// failed call a collection error, and an unknown health value a parse error.
type parityDCGM struct {
	block chan struct{}
	gpus  []*parityGPU
}

func (*parityDCGM) ID() string               { return "gpu.dcgm" }
func (*parityDCGM) Signals() collect.Signals { return fabricSignals }

func (d *parityDCGM) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if d.block != nil {
		<-d.block
	}
	op := collect.NewOp(opts...)
	selected, err := op.Signals(fabricSignals)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool)
	for _, s := range selected {
		want[s.ID()] = true
	}
	cycle := op.Cycle()
	at := timestamppb.New(cycle.StartedAt())
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	failure := func(s collect.Signal, target *observation.ObservationTarget, category observation.CollectionErrorCategory) {
		batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(s.ID(), s.Unit(), "dcgm", "metrics", target, at,
			&observation.CollectionError{Category: category}))
	}
	unsupported := func(s collect.Signal, target *observation.ObservationTarget) {
		if !want[s.ID()] {
			return
		}
		batch.Observations = append(batch.Observations, observation.NewNoValueObservation(s.ID(), s.Unit(), "dcgm", target, at,
			&observation.NoValue{Reason: observation.NoValueReasonNotSupported}))
	}
	value := func(s collect.Signal, target *observation.ObservationTarget, v *observation.Value) {
		if !want[s.ID()] {
			return
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId: s.ID(), Source: "dcgm", Target: target, ObservedAt: at, Unit: s.Unit(),
			Outcome: &observation.Observation_Value{Value: v},
		})
	}
	for _, g := range d.gpus {
		target := observation.GPUTarget(g.uuid)
		switch g.fabricRet {
		case gonvml.SUCCESS:
		case gonvml.ERROR_NOT_SUPPORTED:
			for _, s := range fabricSignals {
				unsupported(s, target)
			}
			continue
		default:
			for _, s := range selected {
				failure(s, target, observation.CollectionErrorCategorySourceError)
			}
			continue
		}
		status := int64(g.fabric.State)
		if g.fabric.State == gonvml.GPU_FABRIC_STATE_COMPLETED {
			status = gpusignals.FabricManagerStatusSuccess
			if gonvml.Return(g.fabric.Status) != gonvml.SUCCESS {
				status = gpusignals.FabricManagerStatusFailure
			}
		}
		value(fabricSignals[0], target, &observation.Value{Kind: &observation.Value_IntValue{IntValue: status}})
		for i, s := range fabricSignals[1:] {
			switch bits := (g.fabric.HealthMask >> (2 * i)) & 0x3; bits {
			case 0:
				unsupported(s, target)
			case 3:
				if want[s.ID()] {
					failure(s, target, observation.CollectionErrorCategoryParseError)
				}
			default:
				value(s, target, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: bits == 1}})
			}
		}
	}
	return batch, nil
}

// healthyFabric is a registered GPU with every health condition clear.
var healthyFabric = gonvml.GpuFabricInfo_v3{
	State: gonvml.GPU_FABRIC_STATE_COMPLETED, Status: uint32(gonvml.SUCCESS), HealthMask: 2 | 2<<2 | 2<<4 | 2<<6,
}

// healthyNode returns n healthy GPUs with the given link count and speed.
func healthyNode(n, links int, speed uint32) []*parityGPU {
	gpus := make([]*parityGPU, n)
	for i := range gpus {
		gpus[i] = &parityGPU{
			uuid: "GPU-" + string(rune('A'+i)), links: links, speed: speed,
			pastEnd: gonvml.ERROR_INVALID_ARGUMENT, fabric: healthyFabric,
		}
	}
	return gpus
}

// legacyNVLink measures gpus, plus failedSlots unresolvable handles, through
// the legacy NVML backend.
func legacyNVLink(t *testing.T, gpus []*parityGPU, failedSlots int, block chan struct{}) *NVMLBackend {
	t.Helper()
	byDevice := make(map[gonvml.Device]*parityGPU)
	handles := make([]gonvml.Device, len(gpus))
	for i, g := range gpus {
		d := &mock.Device{
			GetUUIDFunc:             func() (string, gonvml.Return) { return g.uuid, gonvml.SUCCESS },
			GetFieldValuesFunc:      g.fields,
			GetNvLinkStateFunc:      g.state,
			GetNvLinkCapabilityFunc: g.capability,
			GetP2PStatusFunc: func(peer gonvml.Device, index gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
				return g.p2pStatus(byDevice[peer], index)
			},
		}
		byDevice[d] = g
		handles[i] = d
	}
	orig := deviceFabricInfo
	deviceFabricInfo = func(dev gonvml.Device) (gonvml.GpuFabricInfo_v3, gonvml.Return) {
		return byDevice[dev].fabric, byDevice[dev].fabricRet
	}
	t.Cleanup(func() { deviceFabricInfo = orig })
	return NewWithLibrary(&mock.Interface{
		InitFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			if block != nil {
				// Fail once released, so the abandoned call ends before it
				// reads deviceFabricInfo, which later tests replace.
				<-block
				return 0, gonvml.ERROR_UNKNOWN
			}
			return len(gpus) + failedSlots, gonvml.SUCCESS
		},
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) {
			if i >= len(gpus) {
				return nil, gonvml.ERROR_UNKNOWN
			}
			return handles[i], gonvml.SUCCESS
		},
	})
}

// collectNVLink measures the same GPUs through the collect NVML sensor, the
// DCGM fabric fields, the bridge, and the built-in NVLink adapter. Like the
// app, it requests only the signals the runnable checks on arch require.
func collectNVLink(t *testing.T, arch string, gpus []*parityGPU, failedSlots int, timeout time.Duration, block chan struct{}) []health.Measurement {
	t.Helper()
	inst := &parityInstance{block: block, devices: make(map[string]device.Device)}
	for _, g := range gpus {
		inst.devices[g.uuid] = &collectGPU{gpu: g}
	}
	for i := range failedSlots {
		inst.records = append(inst.records, nvmlsource.InventoryDevice{SourceIndex: uint32(len(gpus) + i), Error: errors.New("device handle unavailable")})
	}
	cycle, err := collect.NewCycleWithTimeout("parity", time.Now(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	required := requiredNVLink(t, arch)
	adapter := collectadapters.NVLink()
	var requested []string
	for id, feeds := range adapter.Signals() {
		if slices.ContainsFunc(feeds, func(s string) bool { return required[s] }) {
			requested = append(requested, id)
		}
	}
	slices.Sort(requested)
	runner := collectbridge.NewRunner(cycle)
	isFabric := func(id string) bool { return slices.Contains(fabricSignals.IDs(), id) }
	if fabric := slices.DeleteFunc(slices.Clone(requested), func(id string) bool { return !isFabric(id) }); len(fabric) > 0 {
		if err = runner.Register(&parityDCGM{block: block, gpus: gpus}, fabric...); err != nil {
			t.Fatal(err)
		}
	}
	if nvmlOwned := slices.DeleteFunc(slices.Clone(requested), isFabric); len(nvmlOwned) > 0 {
		if err = runner.Register(nvmlsensor.New(inst), nvmlOwned...); err != nil {
			t.Fatal(err)
		}
	}
	runner.Collect()
	measurements, err := runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: requested, Category: adapter.Category(), Build: adapter.Build, Errored: adapter.Errored,
	}).Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return measurements
}

// requiredNVLink is the set of NVLink catalog signals the runnable checks on
// arch require.
func requiredNVLink(t *testing.T, arch string) map[string]bool {
	t.Helper()
	runnable, err := catalog.New(catalog.WithArchitecture(arch)).RunnableChecks(catalog.EvaluationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	required := make(map[string]bool)
	for _, check := range runnable {
		if check.Category == health.NVLink {
			for _, s := range check.RequiredSignals {
				required[s] = true
			}
		}
	}
	return required
}

// parityResult is what must not change at the NVLink cutover for one check.
type parityResult struct {
	action    health.RecommendedAction
	severity  health.Severity
	explained bool
}

// nvlinkResults evaluates measurements and returns each NVLink check's
// results across entities, in a stable order.
func nvlinkResults(t *testing.T, arch string, measurements ...health.Measurement) map[string][]parityResult {
	t.Helper()
	verdict, err := catalog.New(catalog.WithArchitecture(arch)).EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]parityResult)
	for _, v := range verdict.CheckResults {
		if v.Category == health.NVLink {
			out[v.CheckID] = append(out[v.CheckID], parityResult{
				action: v.RecommendedAction, severity: v.Severity,
				explained: strings.Contains(strings.Join(v.Messages, " "), "explained by dependency"),
			})
		}
	}
	for _, results := range out {
		slices.SortFunc(results, func(a, b parityResult) int {
			return strings.Compare(fmt.Sprintf("%+v", a), fmt.Sprintf("%+v", b))
		})
	}
	return out
}

// nvlinkFacts renders each measurement's required catalog values, with
// failed signals as "FAILED", keyed by GPU UUID; GPUs without one are keyed by
// order, since the two paths label failed slots differently.
func nvlinkFacts(t *testing.T, required map[string]bool, measurements []health.Measurement) map[string]map[string]any {
	t.Helper()
	out := make(map[string]map[string]any)
	unidentified := 0
	for _, m := range measurements {
		v, ok := m.(nvlink.NVLinkMeasurement)
		if !ok {
			t.Fatalf("unexpected measurement %T", m)
		}
		failed := make(map[string]bool)
		for _, e := range v.Errors() {
			for _, s := range e.FailedSignals() {
				failed[s] = true
			}
		}
		facts := map[string]any{
			"unsupported":   slices.Sorted(slices.Values(v.UnsupportedSignals())),
			"gpu_id failed": failed[nvlink.SignalGPUID],
		}
		value := reflect.ValueOf(v)
		for i := range value.NumField() {
			field := value.Type().Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if !field.IsExported() || !required[name] || name == nvlink.SignalGPUID {
				continue
			}
			facts[name] = value.Field(i).Interface()
			if failed[name] {
				facts[name] = "FAILED"
			}
		}
		key := v.GPUID
		if !strings.HasPrefix(key, "GPU-") || failed[nvlink.SignalGPUID] {
			key = "unidentified-" + string(rune('0'+unidentified))
			unidentified++
		}
		out[key] = facts
	}
	return out
}

// TestNVLinkCollectParity runs the legacy NVML backend and the collect path
// over the same NVML answers. Values, failed signals, unsupported marks,
// severities, actions and dependency notes must match for every NVLink check,
// except the checks a case lists in differs, which must differ. Remove with
// the legacy backend.
func TestNVLinkCollectParity(t *testing.T) {
	type nodeShape struct {
		arch  string
		n     int
		links int
		speed uint32
	}
	gb200 := nodeShape{"GB200", 4, 18, 53125}
	tests := []struct {
		mutate      func(gpus []*parityGPU)
		name        string
		differs     []string
		shape       nodeShape
		failedSlots int
		gpuCount    int64
	}{
		{name: "healthy GB200", shape: gb200},
		{name: "healthy GB300", shape: nodeShape{"GB300", 4, 18, 53125}},
		{name: "healthy B200", shape: nodeShape{"B200", 8, 18, 53125}},
		{name: "healthy H100", shape: nodeShape{"H100", 8, 18, 26562}},
		{name: "healthy A100", shape: nodeShape{"A100", 8, 12, 25000}},
		{name: "inactive link", shape: gb200, mutate: func(g []*parityGPU) { g[1].inactive = map[int]bool{5: true} }},
		{name: "wrong link count", shape: gb200, mutate: func(g []*parityGPU) { g[2].links = 17 }},
		{name: "common speed unsupported", shape: gb200, mutate: func(g []*parityGPU) {
			for _, gpu := range g {
				gpu.speedRet = gonvml.ERROR_NOT_SUPPORTED
			}
		}},
		{name: "common speed unsupported with a slow link", shape: gb200, mutate: func(g []*parityGPU) {
			for _, gpu := range g {
				gpu.speedRet = gonvml.ERROR_NOT_SUPPORTED
			}
			g[0].linkSpeed = map[int]uint32{7: 26562}
		}},
		{name: "unsupported capability", shape: gb200, mutate: func(g []*parityGPU) {
			g[3].capOff = map[int]gonvml.NvLinkCapability{9: gonvml.NVLINK_CAP_SYSMEM_ATOMICS}
		}},
		{name: "state error past link 0 with per-link speeds", shape: gb200, mutate: func(g []*parityGPU) {
			for _, gpu := range g {
				gpu.speedRet = gonvml.ERROR_NOT_SUPPORTED
			}
			g[0].stateRet = map[int]gonvml.Return{3: gonvml.ERROR_UNKNOWN}
		}},
		{name: "unsupported link inside the count", shape: gb200, mutate: func(g []*parityGPU) {
			g[0].stateRet = map[int]gonvml.Return{5: gonvml.ERROR_NOT_SUPPORTED}
		}},
		{name: "fabric read error", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabricRet = gonvml.ERROR_UNKNOWN }},
		// Expected differences. Legacy reads every link index; collect stops
		// at the reported count, so a NOT_SUPPORTED past the last link no
		// longer fails capabilities, and a link past the count is not counted.
		{name: "unsupported past the last link", shape: gb200, differs: []string{"nvlink_capabilities"}, mutate: func(g []*parityGPU) {
			for _, gpu := range g {
				gpu.pastEnd = gonvml.ERROR_NOT_SUPPORTED
			}
		}},
		{name: "disabled link past the reported count", shape: gb200, differs: []string{"nvlink_active"}, mutate: func(g []*parityGPU) {
			g[0].links, g[0].answering, g[0].inactive = 17, 18, map[int]bool{17: true}
		}},
		// Legacy reads health value 3 as false; DCGM reports a parse error.
		{name: "unknown fabric health value", shape: gb200, differs: []string{"nvlink_fabric_health"}, mutate: func(g []*parityGPU) {
			g[0].fabric.HealthMask = 3 | 2<<2 | 2<<4 | 2<<6
		}},
		{name: "unhealthy peer", shape: gb200, mutate: func(g []*parityGPU) {
			g[0].p2p = map[string]gonvml.GpuP2PStatus{g[1].uuid: gonvml.P2P_STATUS_GPU_NOT_SUPPORTED}
		}},
		{name: "P2P query error", shape: gb200, mutate: func(g []*parityGPU) {
			g[0].p2pRet = map[string]gonvml.Return{g[1].uuid: gonvml.ERROR_UNKNOWN}
		}},
		{name: "GPU without NVLink", shape: gb200, mutate: func(g []*parityGPU) {
			g[1].links, g[1].pastEnd, g[1].countRet, g[1].speedRet = 0, gonvml.ERROR_NOT_SUPPORTED, gonvml.ERROR_NOT_SUPPORTED, gonvml.ERROR_NOT_SUPPORTED
			g[1].fabricRet = gonvml.ERROR_NOT_SUPPORTED
		}},
		{name: "failed slot", shape: nodeShape{"GB200", 3, 18, 53125}, failedSlots: 1, gpuCount: 3},
		{name: "fabric not started", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabric.State = gonvml.GPU_FABRIC_STATE_NOT_STARTED }},
		{name: "fabric in progress", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabric.State = gonvml.GPU_FABRIC_STATE_IN_PROGRESS }},
		{name: "fabric registration failed", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabric.Status = uint32(gonvml.ERROR_UNKNOWN) }},
		{name: "bandwidth degraded", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabric.HealthMask = 1 | 2<<2 | 2<<4 | 2<<6 }},
		{name: "route recovery", shape: gb200, mutate: func(g []*parityGPU) { g[1].fabric.HealthMask = 2 | 1<<2 | 2<<4 | 2<<6 }},
		{name: "route unhealthy", shape: gb200, mutate: func(g []*parityGPU) { g[2].fabric.HealthMask = 2 | 2<<2 | 1<<4 | 2<<6 }},
		{name: "access timeout recovery", shape: gb200, mutate: func(g []*parityGPU) { g[3].fabric.HealthMask = 2 | 2<<2 | 2<<4 | 1<<6 }},
		{name: "fabric health condition unsupported", shape: gb200, mutate: func(g []*parityGPU) { g[0].fabric.HealthMask = 2 | 2<<2 | 0<<4 | 2<<6 }},
		{name: "fabric unsupported", shape: gb200, mutate: func(g []*parityGPU) { g[2].fabricRet = gonvml.ERROR_NOT_SUPPORTED }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := test.shape
			gpus := healthyNode(s.n, s.links, s.speed)
			if test.mutate != nil {
				test.mutate(gpus)
			}
			legacy, err := legacyNVLink(t, gpus, test.failedSlots, nil).MeasureNVLink()
			if err != nil {
				t.Fatal(err)
			}
			bridged := collectNVLink(t, s.arch, gpus, test.failedSlots, time.Minute, nil)

			required := requiredNVLink(t, s.arch)
			if l, c := nvlinkFacts(t, required, legacy), nvlinkFacts(t, required, bridged); len(test.differs) == 0 && !reflect.DeepEqual(l, c) {
				t.Errorf("measurements differ:\nlegacy  %v\ncollect %v", l, c)
			}
			var extra []health.Measurement
			if test.gpuCount > 0 {
				extra = append(extra, node.NodeMeasurement{GPUCount: test.gpuCount})
			}
			l := nvlinkResults(t, s.arch, append(legacy, extra...)...)
			c := nvlinkResults(t, s.arch, append(bridged, extra...)...)
			if len(l) == 0 {
				t.Fatal("legacy evaluated no NVLink check")
			}
			for _, id := range test.differs {
				if reflect.DeepEqual(l[id], c[id]) {
					t.Errorf("%s: expected a difference, both %+v", id, l[id])
				}
				delete(l, id)
				delete(c, id)
			}
			if !reflect.DeepEqual(l, c) {
				t.Errorf("verdicts differ:\nlegacy  %+v\ncollect %+v", l, c)
			}
		})
	}
}

// TestNVLinkCollectTimeoutParity stalls both paths past their timeout. Every
// NVLink check must fail with a retry-collection action on both.
func TestNVLinkCollectTimeoutParity(t *testing.T) {
	const timeout = 10 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	gpus := healthyNode(4, 18, 53125)

	legacy, err := toolkit.CollectWithOptions(catalog.New(catalog.WithArchitecture("GB200")), toolkit.CollectionOptions{Timeout: timeout},
		toolkit.NewNVLinkSensor(legacyNVLink(t, gpus, 0, release)))
	if err != nil {
		t.Fatal(err)
	}
	bridged := nvlinkResults(t, "GB200", collectNVLink(t, "GB200", gpus, 0, timeout, release)...)

	retry := health.Drain(health.RemediationActionRetryCollection)
	checked := 0
	for _, v := range legacy.CheckResults {
		if v.Category != health.NVLink {
			continue
		}
		checked++
		if v.Severity != health.Critical || !v.RecommendedAction.Equal(retry) {
			t.Errorf("legacy %s: %s %v, want CRITICAL %v", v.CheckID, v.Severity, v.RecommendedAction, retry)
		}
		for _, c := range bridged[v.CheckID] {
			if c.severity != health.Critical || !c.action.Equal(retry) {
				t.Errorf("collect %s: %s %v, want CRITICAL %v", v.CheckID, c.severity, c.action, retry)
			}
		}
	}
	if checked != 6 || len(bridged) != 6 {
		t.Fatalf("legacy evaluated %d NVLink checks, collect %d; want 6", checked, len(bridged))
	}
}

// TestNVLinkCollectZeroGPUs pins an expected difference: with no GPUs, legacy
// returns no NVLink measurement, so its checks skip, while collect reports the
// missing GPUs and every NVLink check fails with check_collection.
func TestNVLinkCollectZeroGPUs(t *testing.T) {
	legacy, err := legacyNVLink(t, nil, 0, nil).MeasureNVLink()
	if err != nil {
		t.Fatal(err)
	}
	if l := nvlinkResults(t, "GB200", legacy...); len(l) != 0 {
		t.Fatalf("legacy evaluated %v, want no NVLink results", l)
	}
	check := health.Drain(health.RemediationActionCheckCollection)
	bridged := nvlinkResults(t, "GB200", collectNVLink(t, "GB200", nil, 0, time.Minute, nil)...)
	if len(bridged) != 6 {
		t.Fatalf("collect evaluated %d NVLink checks, want 6", len(bridged))
	}
	for id, results := range bridged {
		for _, r := range results {
			if r.severity != health.Critical || !r.action.Equal(check) {
				t.Errorf("collect %s: %s %v, want CRITICAL %v", id, r.severity, r.action, check)
			}
		}
	}
}
