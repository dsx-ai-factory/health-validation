// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	linkflapsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/linkflap"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// nodeFixture is one node's state plus the run's config and flags, rendered
// either into legacy toolkit backends or into collect sources. xid and
// linkFlaps are kernel messages, one minute apart. args must not carry Xid
// window flags, which the collect rendering does not honor.
type nodeFixture struct {
	model        string
	config       string
	xid          []string
	linkFlaps    []string
	args         []string
	gpus         []fixtureGPU
	possibleCPUs int
	onlineCPUs   int
	numaNodes    int
}

type fixtureGPU struct {
	uuid        string
	cmdlines    []string
	powerW      float64
	nvlinkMBps  int64
	inactive    int
	limitW      float64
	enforcedW   float64
	pstate      int64
	slowdown    bool
	thermal     bool
	powerBrake  bool
	pstateFails bool
}

// healthyNode is a node of model with n identical idle GPUs whose UUIDs
// match inventoryReader's, each with fixtureLinks NVLinks at nvlinkMBps.
func healthyNode(model string, n int, limitW float64, numaNodes int, nvlinkMBps int64) nodeFixture {
	f := nodeFixture{model: model, possibleCPUs: 144, onlineCPUs: 144, numaNodes: numaNodes}
	for i := range n {
		f.gpus = append(f.gpus, fixtureGPU{uuid: fmt.Sprintf("GPU-%d", i), powerW: 700, limitW: limitW, enforcedW: limitW, nvlinkMBps: nvlinkMBps})
	}
	return f
}

func healthyGB200() nodeFixture { return healthyNode("NVIDIA GB200", 4, 1200, 34, 53125) }

// fixtureLinks is every fixture GPU's NVLink count, the GB200 and H100
// expectation.
const fixtureLinks = 18

// unhealthyGB200 has a different GPU fault on each GPU, so a swapped signal
// mapping shows, plus an offline CPU and a wrong NUMA count.
func unhealthyGB200() nodeFixture {
	f := healthyGB200()
	f.gpus[0].slowdown = true
	f.gpus[1].thermal = true
	f.gpus[2].powerBrake, f.gpus[2].pstate = true, 8
	f.gpus[3].enforcedW, f.gpus[3].cmdlines = 1000, []string{"python3 train.py"}
	f.onlineCPUs, f.numaNodes = 142, 33
	return f
}

// fixturePID is the PID both renderings give a GPU's i-th compute process.
func fixturePID(i int) uint32 { return uint32(4242 + i) }

func (g fixtureGPU) legacy() gpu.GPUMeasurement {
	lines := make([]string, len(g.cmdlines))
	for i, c := range g.cmdlines {
		lines[i] = fmt.Sprintf("pid=%d %s", fixturePID(i), strconv.Quote(c))
	}
	m := gpu.GPUMeasurement{
		GPUID: g.uuid, AveragePowerDrawW: g.powerW, PowerLimitW: g.limitW, EnforcedPowerLimitW: g.enforcedW,
		GPUPstate: g.pstate, HWSlowdown: g.slowdown, HWThermalSlowdown: g.thermal, HWPowerBrakeSlowdown: g.powerBrake,
		ComputeProcessCount: int64(len(g.cmdlines)), ComputeProcessCmdlines: strings.Join(lines, "\n"),
	}
	if g.pstateFails {
		m.AddError(errors.New("pstate unreadable"), gpu.SignalGPUPstate)
	}
	return m
}

// dcgm is what collect's DCGM sensor reports for g.
func (g fixtureGPU) dcgm(now *timestamppb.Timestamp) []*observation.Observation {
	target := observation.GPUTarget(g.uuid)
	value := func(s collect.Signal, v *observation.Value) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Unit: s.Unit(), Source: "dcgm", Target: target, ObservedAt: now,
			Outcome: &observation.Observation_Value{Value: v},
		}
	}
	double := func(v float64) *observation.Value {
		return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: v}}
	}
	boolean := func(v bool) *observation.Value {
		return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}}
	}
	pstate := value(gpusignals.PerformanceState, &observation.Value{Kind: &observation.Value_IntValue{IntValue: g.pstate}})
	if g.pstateFails {
		pstate = observation.NewCollectionErrorObservation(gpusignals.PerformanceState.ID(), gpusignals.PerformanceState.Unit(), "dcgm", "metrics", target, now,
			&observation.CollectionError{Category: observation.CollectionErrorCategorySourceError, Detail: "pstate unreadable"})
	}
	return []*observation.Observation{
		value(gpusignals.PowerDraw, double(g.powerW)),
		value(gpusignals.PowerLimitRequested, double(g.limitW)),
		value(gpusignals.PowerLimitEnforced, double(g.enforcedW)),
		pstate,
		value(gpusignals.HardwareSlowdownActive, boolean(g.slowdown)),
		value(gpusignals.HardwareThermalSlowdownActive, boolean(g.thermal)),
		value(gpusignals.HardwarePowerBrakeSlowdownActive, boolean(g.powerBrake)),
	}
}

// processes is what collect's NVML sensor reports for g's compute processes.
func (g fixtureGPU) processes(now *timestamppb.Timestamp) []*observation.Observation {
	target := observation.GPUTarget(g.uuid)
	out := []*observation.Observation{{
		SignalId: gpusignals.ProcessCount.ID(), Unit: gpusignals.ProcessCount.Unit(), Source: "nvml", Target: target, ObservedAt: now,
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: uint64(len(g.cmdlines))}}},
	}}
	if len(g.cmdlines) == 0 {
		return append(out, observation.NewNoValueObservation(gpusignals.ProcessCmdline.ID(), gpusignals.ProcessCmdline.Unit(), "nvml", target, now,
			&observation.NoValue{Reason: observation.NoValueReasonNoTargets}))
	}
	for i, c := range g.cmdlines {
		out = append(out, &observation.Observation{
			SignalId: gpusignals.ProcessCmdline.ID(), Unit: gpusignals.ProcessCmdline.Unit(), Source: "nvml",
			Target: observation.GPUProcessTarget(g.uuid, "fixture-boot", fixturePID(i), uint64(i)), ObservedAt: now,
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: c}}},
		})
	}
	return out
}

// legacyNVLink is what the legacy NVLink sensor reports for g among peers:
// every link supports every capability and every P2P pair is OK.
func (g fixtureGPU) legacyNVLink() nvlink.NVLinkMeasurement {
	return nvlink.NVLinkMeasurement{
		GPUID: g.uuid, LinkCount: fixtureLinks, SpeedMBytesPerSec: g.nvlinkMBps, InactiveCount: int64(g.inactive),
		AllLinksSupportP2P: true, AllLinksSupportSysmemAccess: true, AllLinksSupportP2PAtomics: true,
		AllLinksSupportSysmemAtomics: true, AllLinksSupportSLI: true, AllLinksSupportLink: true,
	}
}

// nvlinkObservations is what collect's DCGM and NVML sensors report for g's
// NVLink domain; its first g.inactive links are disabled.
func (g fixtureGPU) nvlinkObservations(now *timestamppb.Timestamp, peers []fixtureGPU) (dcgm, nvml []*observation.Observation) {
	value := func(source string, s collect.Signal, target *observation.ObservationTarget, v *observation.Value) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Unit: s.Unit(), Source: source, Target: target, ObservedAt: now,
			Outcome: &observation.Observation_Value{Value: v},
		}
	}
	uint := func(v uint64) *observation.Value {
		return &observation.Value{Kind: &observation.Value_UintValue{UintValue: v}}
	}
	gpuTarget := observation.GPUTarget(g.uuid)
	dcgm = append(dcgm, value("dcgm", gpusignals.FabricManagerStatus, gpuTarget,
		&observation.Value{Kind: &observation.Value_IntValue{IntValue: gpusignals.FabricManagerStatusSuccess}}))
	nvml = append(nvml,
		value("nvml", gpusignals.NVLinkLinkCount, gpuTarget, uint(fixtureLinks)),
		value("nvml", gpusignals.NVLinkSpeedCommon, gpuTarget, uint(uint64(g.nvlinkMBps))))
	for _, s := range []collect.Signal{
		gpusignals.NVLinkFabricBandwidthDegraded, gpusignals.NVLinkFabricRouteRecoveryActive,
		gpusignals.NVLinkFabricRouteUnhealthy, gpusignals.NVLinkFabricAccessTimeoutRecovery,
	} {
		nvml = append(nvml, value("nvml", s, gpuTarget, &observation.Value{Kind: &observation.Value_BoolValue{}}))
	}
	for link := range uint32(fixtureLinks) {
		target := observation.NVLinkEndpointTarget(g.uuid, link)
		nvml = append(nvml, value("nvml", gpusignals.NVLinkState, target, uint(boolToUint(int(link) >= g.inactive))))
		for _, s := range gpusignals.EndpointCapabilitySignals() {
			nvml = append(nvml, value("nvml", s, target, uint(1)))
		}
	}
	for _, peer := range peers {
		if peer.uuid != g.uuid {
			nvml = append(nvml, value("nvml", gpusignals.NVLinkP2PStatus, observation.GPUPairTarget(g.uuid, peer.uuid), uint(0)))
		}
	}
	return dcgm, nvml
}

func boolToUint(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// fixedNVLink is a legacy NVLink backend that reports fixed measurements.
type fixedNVLink []health.Measurement

func (b fixedNVLink) MeasureNVLink() ([]health.Measurement, error) { return b, nil }

// fixedSensor serves fixed observations for the signals the run requested.
type fixedSensor struct {
	id      string
	signals collect.Signals
	obs     []*observation.Observation
}

func (s fixedSensor) ID() string               { return s.id }
func (s fixedSensor) Signals() collect.Signals { return s.signals }

func (s fixedSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.signals)
	if err != nil {
		return nil, err
	}
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	for _, o := range s.obs {
		if slices.Contains(selected.IDs(), o.GetSignalId()) {
			batch.Observations = append(batch.Observations, o)
		}
	}
	return batch, nil
}

// cpuFS is the sysfs collect's CPU sensor reads. Counts must be at least 1.
func cpuFS(possible, online, numa int) fstest.MapFS {
	list := func(n int) *fstest.MapFile { return &fstest.MapFile{Data: fmt.Appendf(nil, "0-%d\n", n-1)} }
	return fstest.MapFS{
		"sys/devices/system/cpu/possible": list(possible),
		"sys/devices/system/cpu/online":   list(online),
		"sys/devices/system/node/online":  list(numa),
	}
}

// fixtureEntryTime is when the fixture journal stamps its i-th entry: inside
// the link-flap lookback and one minute apart, so consecutive link-flap lines
// are an outage longer than min_outage_seconds.
func fixtureEntryTime(start time.Time, i int) time.Time {
	return start.Add(time.Duration(i) * time.Minute)
}

const fixtureJournalPrelude = "usb 1-1: new high-speed USB device number 2"

// fixtureJournal stands in for journalctl, printing lines as kernel entries of
// the current boot. The leading unrelated entry keeps a clean fixture from
// failing as an empty journal; the legacy mocks ignore it.
func fixtureJournal(lines []string) journal.Runner {
	start := time.Now().Add(-30 * time.Minute)
	entries := append([]string{fixtureJournalPrelude}, lines...)
	return func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		for i, m := range entries {
			message, err := json.Marshal(m)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stdout, `{"__CURSOR":"s=1;i=%d","__REALTIME_TIMESTAMP":"%d","_BOOT_ID":"0123456789abcdef0123456789abcdef","MESSAGE":%s}`+"\n",
				i+1, fixtureEntryTime(start, i).UnixMicro(), message)
		}
		return nil
	}
}

// legacyLinkFlaps renders lines as the short-full journalctl output the
// legacy link-flap backend parses, with fixtureJournal's spacing.
func legacyLinkFlaps(lines []string) []string {
	start := time.Now().Add(-30 * time.Minute)
	out := make([]string, len(lines))
	for i, m := range lines {
		out[i] = fixtureEntryTime(start, i+1).Format("Mon 2006-01-02 15:04:05 MST") + " host kernel: " + m
	}
	return out
}

// Renderings for fixtureApp and runFixture.
const (
	legacyPath  = false
	collectPath = true
)

// fixtureApp renders f into an App whose domains come from legacy backends
// or, with fromCollect, from collect sources.
func fixtureApp(f nodeFixture, fromCollect bool) *App {
	source := healthyRDMATopoSensor()
	a := New().WithProgramName("test").WithCollect(source, source.Signals().IDs()...)
	// A config with overrides is audited to journald first; without a
	// journal the run would fail closed.
	var rec datagramRecorder
	a.openJournal = func() (io.WriteCloser, error) { return &rec, nil }
	host := sharedInventoryHost{
		fakeHost:  fakeHost{b: spyBackend{called: new(bool)}},
		inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return inventoryReader{gpus: len(f.gpus), model: f.model}, nil }),
	}
	if fromCollect {
		now := timestamppb.Now()
		var dcgm, nvml []*observation.Observation
		for _, g := range f.gpus {
			linkDCGM, linkNVML := g.nvlinkObservations(now, f.gpus)
			dcgm = append(append(dcgm, g.dcgm(now)...), linkDCGM...)
			nvml = append(append(nvml, g.processes(now)...), linkNVML...)
		}
		host.gpu = []collect.Sensor{
			fixedSensor{id: "gpu.dcgm", signals: slices.Concat(dcgmGPUSignals, dcgmNVLinkSignals), obs: dcgm},
			fixedSensor{id: "gpu.nvml", signals: slices.Concat(processGPUSignals, nvmlNVLinkSignals), obs: nvml},
		}
		a.WithCollect(cpusensor.New(cpuFS(f.possibleCPUs, f.onlineCPUs, f.numaNodes)), observations.MappedSignalsFor(health.CPU)...)
		a.WithCollect(xidsensor.New(journal.New(fixtureJournal(f.xid))), xidsignals.All().IDs()...)
		a.WithCollect(linkflapsensor.New(journal.New(fixtureJournal(f.linkFlaps)), linkflapsensor.WithLookback(linkFlapLookback)),
			ibsignals.LinkFlapSignals().IDs()...)
	} else {
		gpus := make([]gpu.GPUMeasurement, len(f.gpus))
		links := make(fixedNVLink, len(f.gpus))
		for i, g := range f.gpus {
			gpus[i] = g.legacy()
			links[i] = g.legacyNVLink()
		}
		a.WithGPUBackend(toolkit.MockGPU(gpus...))
		a.WithNVLinkBackend(links)
		a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap(legacyLinkFlaps(f.linkFlaps)...))
		a.WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{GPUCount: int64(len(f.gpus))}))
		a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{
			PossibleCPUCount: int64(f.possibleCPUs), OnlineCPUCount: int64(f.onlineCPUs), NUMANodeCount: int64(f.numaNodes),
		}))
		a.WithXidBackend(toolkit.MockXid(append([]string{fixtureJournalPrelude}, f.xid...)...))
	}
	a.host = host
	return a
}

// runFixture runs the CLI over f and returns its normalized verdict and exit
// code.
func runFixture(t *testing.T, f nodeFixture, fromCollect bool) (health.NodeVerdict, int) {
	t.Helper()
	args := append([]string{"run", "--node-id", testNodeID}, f.args...)
	if f.config != "" {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(f.config), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--config", path)
	}
	var stdout, stderr bytes.Buffer
	code := fixtureApp(f, fromCollect).Run(args, &stdout, &stderr)
	if code == exitError {
		t.Fatalf("Run exit = %d; stderr = %q", code, stderr.String())
	}
	return normalizeVerdict(t, stdout.Bytes()), code
}

// normalizeVerdict clears what differs between runs and orders what
// producers may order differently, such as per-entity results. It also clears
// UNKNOWN results' messages: collect names the failing source and operation.
func normalizeVerdict(t *testing.T, raw []byte) health.NodeVerdict {
	t.Helper()
	var v health.NodeVerdict
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, raw)
	}
	v.Timestamp, v.TraceID, v.CatalogVersion = time.Time{}, "", ""
	for _, results := range [][]health.CheckVerdict{v.CheckResults, v.SkippedChecks} {
		for i := range results {
			results[i].Timestamp = time.Time{}
			results[i].TraceID, results[i].SpanID = "", ""
			if results[i].Severity == health.Unknown {
				results[i].Messages = nil
			}
		}
		slices.SortStableFunc(results, func(a, b health.CheckVerdict) int {
			if c := strings.Compare(a.CheckID, b.CheckID); c != 0 {
				return c
			}
			aj, _ := json.Marshal(a)
			bj, _ := json.Marshal(b)
			return bytes.Compare(aj, bj)
		})
	}
	slices.SortFunc(v.AffectedComponents, func(a, b health.AffectedComponent) int {
		return strings.Compare(a.Category.String(), b.Category.String())
	})
	for i := range v.AffectedComponents {
		slices.Sort(v.AffectedComponents[i].CheckIDs)
		slices.Sort(v.AffectedComponents[i].Entities)
	}
	return v
}

// verdictDiff shows the first line where a and b differ as indented JSON, or
// "" when they match.
func verdictDiff(a, b health.NodeVerdict) string {
	aj, _ := json.MarshalIndent(a, "", "  ")
	bj, _ := json.MarshalIndent(b, "", "  ")
	al, bl := strings.Split(string(aj), "\n"), strings.Split(string(bj), "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n- %s\n+ %s", i+1, x, y)
		}
	}
	return ""
}
