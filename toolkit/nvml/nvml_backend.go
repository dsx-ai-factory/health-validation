// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// NVMLBackend is the NVML-backed data source. It owns the library handle
// and the lazily-enumerated, cached device list, and is a full multi-domain
// source — it implements MeasureGPUs (per-GPU signals), MeasureNode
// (host-wide gpu_count), and MeasureNVLink (per-GPU NVLink signals), so it
// satisfies all three toolkit per-domain backend interfaces.
//
// Devices whose handle cannot be retrieved are represented internally by an
// erroredDevice; the measure methods turn those slots — and any per-GPU
// query failure encountered while collecting signals — into errored
// measurements so the catalog can emit failed verdicts for the affected
// GPUs instead of silently dropping them.
//
// The per-GPU reads are pluggable per domain: MeasureGPUs and MeasureNVLink
// each run their own [DeviceProber] over that backend's one device
// enumeration. By default
// MeasureGPUs emits the built-in per-GPU signals (UUID, performance state) and
// MeasureNVLink emits the built-in fabric/links/active signals; a downstream
// that needs extra per-GPU signals swaps in its own prober per domain with
// [NVMLBackend.WithGPUProbe] / [NVMLBackend.WithNVLinkProbe]. The backend
// still owns NVML init/shutdown, device enumeration, and errored-slot
// detection. MeasureNode is a scalar aggregate (gpu_count) with no probe.
//
// NVML init is lazy: New/NewWithLibrary are side-effect free and the library
// opens on the first measurement. Constructing a backend is therefore safe
// on a host without a GPU/driver — an init failure surfaces from the first
// Measure, not from construction. Call Close() when done (a no-op if the
// library was never opened).
type NVMLBackend struct {
	lib          gonvml.Interface                 // nil until the first use opens it
	gpuProber    DeviceProber                     // per-GPU read for MeasureGPUs
	nvlinkProber DeviceProber                     // per-GPU read for MeasureNVLink
	open         func() (gonvml.Interface, error) // opens+inits the library on first use
	devs         []gonvml.Device
}

// New returns a backend that opens NVML via libnvidia-ml.so on first use.
// Requires a real GPU host at runtime; use NewWithLibrary to inject a mock
// for testing.
func New() *NVMLBackend {
	return &NVMLBackend{
		open:         func() (gonvml.Interface, error) { return initLib(gonvml.New()) },
		gpuProber:    gpuProbe{},
		nvlinkProber: nvlinkProbe{},
	}
}

// NewWithLibrary returns a backend that opens NVML via the provided interface
// on first use. Pass a mock (github.com/NVIDIA/go-nvml/pkg/nvml/mock) to test
// without real GPU hardware.
func NewWithLibrary(lib gonvml.Interface) *NVMLBackend {
	return &NVMLBackend{
		open:         func() (gonvml.Interface, error) { return initLib(lib) },
		gpuProber:    gpuProbe{},
		nvlinkProber: nvlinkProbe{},
	}
}

// initLib initializes the NVML library, returning it ready for use.
func initLib(lib gonvml.Interface) (gonvml.Interface, error) {
	if ret := lib.Init(); ret != gonvml.SUCCESS {
		return nil, fmt.Errorf("nvml init: %s", ret)
	}
	return lib, nil
}

// ensure opens the library on first use. Subsequent calls are no-ops.
func (b *NVMLBackend) ensure() error {
	if b.lib != nil {
		return nil
	}
	lib, err := b.open()
	if err != nil {
		return err
	}
	b.lib = lib
	return nil
}

// WithGPUProbe returns a copy of the backend whose MeasureGPUs uses p instead
// of the built-in identity prober. It is domain-scoped: the NVLink prober and
// the node read are preserved. Call it at setup and use the returned backend.
//
// The returned backend is independent of the receiver: because NVML opens
// lazily, it opens its own connection — and thus its own device enumeration —
// on first use rather than sharing the receiver's. This guarantee holds only
// when WithGPUProbe is called before any measurement on the receiver (i.e.
// b.lib == nil); if called after a measurement, the copy inherits the open
// connection and shares the receiver's NVML session. Drive every domain you
// want from a single backend instance (e.g. install one backend under both the
// GPU and NVLink setters) to keep them on a single enumeration.
//
//	b := nvml.New().WithGPUProbe(eccProbe{})
func (b *NVMLBackend) WithGPUProbe(p DeviceProber) *NVMLBackend {
	cp := *b
	cp.gpuProber = p
	return &cp
}

// WithNVLinkProbe returns a copy of the backend whose MeasureNVLink uses p
// instead of the built-in NVLink prober. Like [NVMLBackend.WithGPUProbe] it is
// domain-scoped — the GPU prober and the node read are preserved — so the two
// can be chained to customize both per-GPU domains over one connection:
//
//	b := nvml.New().WithGPUProbe(eccProbe{}).WithNVLinkProbe(myNVLinkProbe{})
func (b *NVMLBackend) WithNVLinkProbe(p DeviceProber) *NVMLBackend {
	cp := *b
	cp.nvlinkProber = p
	return &cp
}

// devices returns the handles for all GPUs discovered by NVML.
// Handles are fetched on first call and cached on the receiver for
// subsequent calls. A device whose handle cannot be retrieved is
// represented by an [erroredDevice] in the returned slice carrying
// the failing Return code, so the broken slot stays visible to Measure
// (which renders it as an errored measurement) instead of being
// silently dropped.
//
// Only DeviceGetCount failures surface as an error from this method;
// per-device handle failures degrade to erroredDevice entries. The
// method is unexported because callers should reach the device list
// only through Measure — the slice carries internal placeholder types
// that are not part of the public contract.
func (b *NVMLBackend) devices() ([]gonvml.Device, error) {
	if b.devs != nil {
		return b.devs, nil
	}
	if err := b.ensure(); err != nil {
		return nil, err
	}
	count, ret := b.lib.DeviceGetCount()
	if ret != gonvml.SUCCESS {
		return nil, &Error{Op: "DeviceGetCount", Return: ret}
	}
	devs := make([]gonvml.Device, count)
	for i := range devs {
		dev, ret := b.lib.DeviceGetHandleByIndex(i)
		if ret == gonvml.SUCCESS {
			devs[i] = dev
		} else {
			devs[i] = erroredDevice{index: i, ret: ret}
		}
	}
	b.devs = devs
	return b.devs, nil
}

// Close shuts down the NVML library. It is a no-op if the library was never
// opened (no measurement was ever taken).
func (b *NVMLBackend) Close() error {
	if b.lib == nil {
		return nil
	}
	if ret := b.lib.Shutdown(); ret != gonvml.SUCCESS {
		return fmt.Errorf("nvml shutdown: %s", ret)
	}
	return nil
}

// MeasureGPUs returns one [health.Measurement] per device by running the
// configured GPU [DeviceProber] over the enumeration (the built-in [gpuProbe]
// by default, or a downstream's via [NVMLBackend.WithGPUProbe]). Slots whose
// handle failed to load and GPUs whose UUID query fails are routed to the
// prober's Errored callback with a pre-computed id, so the catalog can emit
// failed verdicts for the affected GPUs instead of failing the whole pass.
func (b *NVMLBackend) MeasureGPUs() ([]health.Measurement, error) {
	return b.probe(b.gpuProber)
}

// A ProbeDevice is an NVML device that was successfully enumerated and
// identified by the backend: a live device handle along with its UUID and
// NVML enumeration index.
type ProbeDevice struct {
	Device gonvml.Device
	UUID   string
	Index  int
}

// A ProbeContext is the input to one Measure call. Self is the device being
// measured. Peers holds the other usable devices from the same enumeration.
//
// Peers never contains Self or a device that failed to resolve, so every
// handle in it is safe to query. Because failed devices are left out, peer
// Index values may have gaps. The Peers slice is newly allocated for each
// Measure call.
type ProbeContext struct {
	Self  ProbeDevice
	Peers []ProbeDevice
}

// DeviceProber maps one enumerated NVML device to one [health.Measurement].
// It is the seam for adding NVML-backed checks: the [NVMLBackend] owns NVML
// init/shutdown, device enumeration, errored-slot detection, and the
// "nvml-index-N" id-encoding, and drives the per-device loop via MeasureGPUs
// or MeasureNVLink, calling back into the relevant prober once per slot. The
// implementer writes only the per-device read — no lifecycle, no loop, no
// enumeration error handling.
//
// A prober lives with the backend (it calls go-nvml device methods, so it is
// cgo-bound); a downstream installs its own per domain via
// [NVMLBackend.WithGPUProbe] / [NVMLBackend.WithNVLinkProbe].
//
//   - Measure is called for a usable slot (handle loaded, UUID read) with a
//     [ProbeContext] carrying the device and its usable peers, so both
//     per-device and cross-device signals can be read.
//   - Errored is called for a slot the backend could not fully resolve
//     (its handle failed to load, or its UUID could not be read), with the
//     pre-computed id "nvml-index-N". The backend keys a built-in
//     [gpu.GPUMeasurement] or [nvlink.NVLinkMeasurement] it returns as
//     [health.ProvisionalEntity]("nvml", "N"); any other type is keyed by its
//     own [health.EntityKeyed] or EntityID.
type DeviceProber interface {
	Measure(pc ProbeContext) health.Measurement
	Errored(id string, err error) health.Measurement
}

// probe enumerates devices and turns each into a measurement by calling back
// into p once per slot: Errored for a slot whose handle or UUID could not be
// read (id pre-computed as "nvml-index-N"), and Measure for a usable slot. Every slot is resolved
// before the first Measure fires, because a slot's Peers cannot be built
// until the usable set is fully known. The only error enumeration returns —
// a DeviceGetCount failure — propagates unchanged.
//
// MeasureGPUs and MeasureNVLink both delegate here so the per-device loop is a
// single seam: a future decomposition can lift this verbatim onto a shared
// session.
func (b *NVMLBackend) probe(p DeviceProber) ([]health.Measurement, error) {
	devices, err := b.devices()
	if err != nil {
		return nil, err
	}

	measurements := make([]health.Measurement, len(devices))
	usable := make([]ProbeDevice, 0, len(devices))
	for i, dev := range devices {
		// An unresolved slot has no UUID, so it is named by its NVML index.
		if e, ok := dev.(erroredDevice); ok {
			measurements[i] = slotErrored(p, e.index, &Error{Op: "DeviceGetHandleByIndex", Return: e.ret})
			continue
		}
		uuid, ret := dev.GetUUID()
		if ret != gonvml.SUCCESS {
			measurements[i] = slotErrored(p, i, &Error{Op: "DeviceGetUUID", Return: ret})
			continue
		}
		usable = append(usable, ProbeDevice{Device: dev, UUID: uuid, Index: i})
	}

	for _, d := range usable {
		measurements[d.Index] = p.Measure(ProbeContext{
			Self:  d,
			Peers: getPeers(usable, d.Index),
		})
	}
	return measurements, nil
}

// nvmlSource namespaces NVML slot indexes in provisional entity keys.
const nvmlSource = "nvml"

// slotErrored builds p's errored measurement for the slot at index, keyed by
// that slot so it cannot be mistaken for another source's slot or a UUID.
func slotErrored(p DeviceProber, index int, err error) health.Measurement {
	m := p.Errored(fmt.Sprintf("nvml-index-%d", index), err)
	key := health.ProvisionalEntity(nvmlSource, strconv.Itoa(index))
	switch v := m.(type) {
	case gpu.GPUMeasurement:
		v.SetEntityKey(key)
		return v
	case nvlink.NVLinkMeasurement:
		v.SetEntityKey(key)
		return v
	}
	return m
}

// getPeers returns the usable devices except the one whose enumeration
// index is self. The slice is freshly allocated per call. The exclusion keys
// on Index because it is unique by construction, unlike UUIDs, which a
// misbehaving driver can duplicate.
func getPeers(usable []ProbeDevice, self int) []ProbeDevice {
	peers := make([]ProbeDevice, 0, len(usable)-1)
	for _, d := range usable {
		if d.Index != self {
			peers = append(peers, d)
		}
	}
	return peers
}

// gpuProbe is the built-in GPU prober: it reads the per-GPU health
// signals into one [gpu.GPUMeasurement] per device.
// Slots the backend could not resolve become [gpu.ErroredGPU].
type gpuProbe struct{}

func (gpuProbe) Measure(pc ProbeContext) health.Measurement {
	m := gpu.GPUMeasurement{GPUID: pc.Self.UUID}
	measureDevicePstate(pc.Self.Device, &m)
	measureDeviceAveragePowerDraw(pc.Self.Device, &m)
	measureDevicePowerLimits(pc.Self.Device, &m)
	measureDeviceThrottle(pc.Self.Device, &m)
	measureDeviceComputeProcesses(pc.Self.Device, &m)
	return m
}

// measureDevicePstate sets the GPU's current performance state on m as
// the numeric level N in P<N> (0 = P0, maximum performance).
//
// Any non-SUCCESS return, including ERROR_NOT_SUPPORTED, flags
// gpu_pstate, as does a PSTATE_UNKNOWN reading.
func measureDevicePstate(dev gonvml.Device, m *gpu.GPUMeasurement) {
	state, ret := dev.GetPerformanceState()
	if ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetPerformanceState", Return: ret},
			gpu.SignalGPUPstate)
		return
	}
	if state == gonvml.PSTATE_UNKNOWN {
		// The driver cannot determine the state
		m.AddError(fmt.Errorf("nvml DeviceGetPerformanceState: performance state unknown"),
			gpu.SignalGPUPstate)
		return
	}
	m.GPUPstate = int64(state)
}

const milliwattsPerWatt = 1000

// measureDeviceAveragePowerDraw sets the GPU's one-second average power
// draw on m, in watts, from NVML's milliwatt FI_DEV_POWER_AVERAGE field.
//
// The field API returns a per-field NvmlReturn independent of the call's
// own return, and reports the value as an untyped 8-byte union, so a
// SUCCESS call can still carry a failed field or an unexpected ValueType;
// both are treated as collection failures alongside the call itself.
func measureDeviceAveragePowerDraw(dev gonvml.Device, m *gpu.GPUMeasurement) {
	values := []gonvml.FieldValue{{
		FieldId: gonvml.FI_DEV_POWER_AVERAGE,
		ScopeId: 0,
	}}
	if ret := dev.GetFieldValues(values); ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetFieldValues(POWER_AVERAGE)", Return: ret},
			gpu.SignalAveragePowerDrawW)
		return
	}

	value := values[0]
	if ret := gonvml.Return(value.NvmlReturn); ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetFieldValues(POWER_AVERAGE)", Return: ret},
			gpu.SignalAveragePowerDrawW)
		return
	}
	if gonvml.ValueType(value.ValueType) != gonvml.VALUE_TYPE_UNSIGNED_INT {
		m.AddError(fmt.Errorf("nvml DeviceGetFieldValues(POWER_AVERAGE): unexpected ValueType %d, want UNSIGNED_INT", value.ValueType),
			gpu.SignalAveragePowerDrawW)
		return
	}
	m.AveragePowerDrawW = float64(binary.LittleEndian.Uint32(value.Value[:4])) / milliwattsPerWatt
}

func measureDevicePowerLimits(dev gonvml.Device, m *gpu.GPUMeasurement) {
	limit, ret := dev.GetPowerManagementLimit()
	if ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetPowerManagementLimit", Return: ret},
			gpu.SignalPowerLimitW)
	} else {
		m.PowerLimitW = float64(limit) / milliwattsPerWatt
	}

	enforced, ret := dev.GetEnforcedPowerLimit()
	if ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetEnforcedPowerLimit", Return: ret},
			gpu.SignalEnforcedPowerLimitW)
	} else {
		m.EnforcedPowerLimitW = float64(enforced) / milliwattsPerWatt
	}
}

// measureDeviceThrottle sets the GPU's hardware clocks-event-reasons bits
// on m. Unlike measureDevicePstate/measureDevicePowerLimits,
// ERROR_NOT_SUPPORTED leaves the signals at their false zero value rather
// than flagging an error: a device that cannot report clocks-event-reasons
// at all should not be treated as actively throttling.
func measureDeviceThrottle(dev gonvml.Device, m *gpu.GPUMeasurement) {
	mask, ret := dev.GetCurrentClocksEventReasons()
	switch ret {
	case gonvml.SUCCESS:
		m.HWSlowdown = mask&gonvml.ClocksThrottleReasonHwSlowdown != 0
		m.HWThermalSlowdown = mask&gonvml.ClocksThrottleReasonHwThermalSlowdown != 0
		m.HWPowerBrakeSlowdown = mask&gonvml.ClocksThrottleReasonHwPowerBrakeSlowdown != 0
	case gonvml.ERROR_NOT_SUPPORTED:
		m.MarkUnsupported(gpu.SignalHWSlowdown, gpu.SignalHWThermalSlowdown, gpu.SignalHWPowerBrakeSlowdown)
	default:
		m.AddError(&Error{Op: "DeviceGetCurrentClocksEventReasons", Return: ret},
			gpu.SignalHWSlowdown,
			gpu.SignalHWThermalSlowdown,
			gpu.SignalHWPowerBrakeSlowdown)
	}
}

// procCmdlineMax is the diagnostic cap on /proc/<pid>/cmdline after NUL
// bytes are turned into spaces. Fail/pass uses process count, not this text.
const procCmdlineMax = 250

func measureDeviceComputeProcesses(dev gonvml.Device, m *gpu.GPUMeasurement) {
	infos, ret := dev.GetComputeRunningProcesses()
	if ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetComputeRunningProcesses", Return: ret},
			gpu.SignalComputeProcessCount,
			gpu.SignalComputeProcessCmdlines)
		return
	}
	var parts []string
	var n int64
	for _, p := range infos {
		if p.Pid == 0 {
			continue
		}
		n++
		parts = append(parts, formatComputeProcessLine(p.Pid, readProcCmdline(p.Pid)))
	}
	m.ComputeProcessCount = n
	// One process per line. Each cmdline is strconv.Quote'd so an
	// argument that contains a newline cannot split the diagnostic.
	m.ComputeProcessCmdlines = strings.Join(parts, "\n")
}

func formatComputeProcessLine(pid uint32, cmdline string) string {
	if cmdline == "" {
		return fmt.Sprintf("pid=%d", pid)
	}
	return fmt.Sprintf("pid=%d %s", pid, strconv.Quote(cmdline))
}

func readProcCmdline(pid uint32) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return decodeProcCmdline(b, procCmdlineMax)
}

func decodeProcCmdline(raw []byte, max int) string {
	s := strings.ReplaceAll(string(bytes.TrimRight(raw, "\x00")), "\x00", " ")
	if max > 0 && len(s) > max {
		return s[:max]
	}
	return s
}

func (gpuProbe) Errored(id string, err error) health.Measurement {
	return gpu.ErroredGPU(id, err, gpu.MeasurementSignals()...)
}

// MeasureNode returns one [node.NodeMeasurement] populated with the
// visible gpu_count. A DeviceGetCount failure flags every signal in
// [node.MeasurementSignals()] (no telemetry is trustworthy); a node-level
// check fails Critical only when one of its RequiredSignals overlaps
// with the failed entry.
//
// gpu_count counts only devices whose handle could be retrieved — slots
// that fell off the bus (DeviceGetHandleByIndex failed) are excluded.
// GPUs that were never enumerated at all (e.g. PCIe link dead at POST)
// and slots whose handle load failed both push this count below the
// operator's expected_gpu_count. Per-GPU NVLink state is collected by
// MeasureNVLink, not here.
func (b *NVMLBackend) MeasureNode() ([]health.Measurement, error) {
	var m node.NodeMeasurement

	devs, err := b.devices()
	if err != nil {
		// devices() only fails on DeviceGetCount failure; the
		// typed *Error travels through unchanged.
		m.AddError(err, node.MeasurementSignals()...)
		return []health.Measurement{m}, nil
	}

	var visible int64
	for _, d := range devs {
		// A slot whose handle failed to load is excluded from the
		// visible count; the gpu_count check surfaces the shortfall
		// against expected_gpu_count.
		if _, errored := d.(erroredDevice); errored {
			continue
		}
		visible++
	}
	m.GPUCount = visible

	return []health.Measurement{m}, nil
}

// erroredDevice is a placeholder gonvml.Device for a slot whose handle
// could not be retrieved. The embedded gonvml.Device is intentionally
// nil — Measure detects erroredDevice via type assertion before any
// query method runs, so methods on the embedded interface are never
// invoked. Keeping the failing slot in the device list (instead of
// dropping it) lets Measure emit an errored measurement so downstream
// consumers can see which GPU went unobserved.
type erroredDevice struct {
	gonvml.Device
	ret   gonvml.Return
	index int
}

// MeasureNVLink returns one [health.Measurement] per device by running the
// configured NVLink [DeviceProber] over the enumeration (the built-in
// [nvlinkProbe] by default, or a downstream's via [NVMLBackend.WithNVLinkProbe]).
// It shares the same per-device loop as MeasureGPUs, so errored slots are
// handled identically.
func (b *NVMLBackend) MeasureNVLink() ([]health.Measurement, error) {
	return b.probe(b.nvlinkProber)
}

// nvlinkProbe is the built-in NVLink prober: it reads fabric, link, and
// active-link state per device into one [nvlink.NVLinkMeasurement]. Slots the
// backend could not resolve become [nvlink.ErroredNVLink].
type nvlinkProbe struct{}

func (nvlinkProbe) Measure(pc ProbeContext) health.Measurement {
	m := nvlink.NVLinkMeasurement{GPUID: pc.Self.UUID}
	measureDeviceFabric(pc.Self.Device, &m)
	measureDeviceLinks(pc.Self.Device, &m)
	measureDeviceActiveLinks(pc.Self.Device, &m)
	measureDeviceLinkCapabilities(pc.Self.Device, &m)
	measureDeviceP2P(pc, &m)
	return m
}

func (nvlinkProbe) Errored(id string, err error) health.Measurement {
	return nvlink.ErroredNVLink(id, err, nvlink.MeasurementSignals()...)
}

// measureDeviceP2P counts the peer GPUs this device cannot reach over
// NVLink P2P onto m.UnhealthyP2PPeerCount.
//
// It probes DeviceGetP2PStatus(peer, P2P_CAPS_INDEX_NVLINK) for every
// device in pc.Peers. A status other than P2P_STATUS_OK means the pair
// cannot peer over NVLink and counts as one unhealthy peer. Unresolvable
// slots never appear in pc.Peers, so they are not counted here — their
// own measurement already fails Critical.
//
// If a query fails, the signal is flagged and the function returns
// without writing the count; the errored signal drives the verdict
// Critical.
func measureDeviceP2P(pc ProbeContext, m *nvlink.NVLinkMeasurement) {
	var unhealthy int64
	for _, peer := range pc.Peers {
		status, ret := pc.Self.Device.GetP2PStatus(peer.Device, gonvml.P2P_CAPS_INDEX_NVLINK)
		if ret != gonvml.SUCCESS {
			m.AddError(&Error{Op: "DeviceGetP2PStatus", Return: ret},
				nvlink.SignalUnhealthyP2PPeerCount)
			return
		}
		if status != gonvml.P2P_STATUS_OK {
			unhealthy++
		}
	}
	m.UnhealthyP2PPeerCount = unhealthy
}

// deviceFabricInfo reads a device's versioned GPU fabric info. It is a
// package variable because the real GpuFabricInfoHandler.V3() calls
// libnvidia-ml directly and cannot be routed through gonvml.Interface.
var deviceFabricInfo = func(dev gonvml.Device) (gonvml.GpuFabricInfo_v3, gonvml.Return) {
	return dev.GetGpuFabricInfoV().V3()
}

// measureDeviceFabric sets the NVLink fabric signals on m from this
// device's versioned GPU fabric info, read once and decoded into the
// health-flag signals (from the health bitmask) and the registration
// signals (from the probe State and Status).
//
// Health-flag mapping (from GpuFabricInfo_v3.HealthMask, each a 2-bit
// field where _TRUE (1) flags the signal and _FALSE (2)/_NOT_SUPPORTED
// (0) leave it unchanged):
//   - fabric_bandwidth_degraded         ← DEGRADED_BW field == _TRUE
//   - fabric_route_recovery_in_progress ← ROUTE_RECOVERY field == _TRUE
//   - fabric_route_unhealthy            ← ROUTE_UNHEALTHY field == _TRUE
//   - fabric_access_timeout_recovery    ← ACCESS_TIMEOUT_RECOVERY field == _TRUE
//
// Registration mapping (from State/Status):
//   - fabric_registration_incomplete ← State != COMPLETED
//   - fabric_registration_failed     ← State == COMPLETED but Status != SUCCESS
//
// The check is scoped to GB200/GB300, where every GPU must join the NVLink
// fabric, so a GPU reporting NOT_SUPPORTED — whether as a SUCCESS query
// with State NOT_SUPPORTED or an ERROR_NOT_SUPPORTED return — is flagged
// as registration incomplete while its health-mask signals stay neutral.
// Status is defined only once State reports COMPLETED, so a probe that
// has not completed never sets the failed signal. Any other failure
// records one error entry covering all fabric signals.
func measureDeviceFabric(dev gonvml.Device, m *nvlink.NVLinkMeasurement) {
	info, ret := deviceFabricInfo(dev)
	switch ret {
	case gonvml.SUCCESS:
		field := func(shift, mask uint32) uint32 { return (info.HealthMask >> shift) & mask }
		m.FabricBandwidthDegraded = field(
			gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW,
			gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW,
		) == gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_TRUE
		m.FabricRouteRecoveryInProgress = field(
			gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY,
			gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY,
		) == gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_TRUE
		m.FabricRouteUnhealthy = field(
			gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY,
			gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY,
		) == gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE
		m.FabricAccessTimeoutRecovery = field(
			gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY,
			gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY,
		) == gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_TRUE
		// Each health condition is reported as true, false, or not
		// supported by this GPU and driver. Like the clock-event bits, an
		// unsupported condition stays false rather than failing the check;
		// it is marked so an export can tell it apart from a reported false.
		for _, bit := range []struct {
			signal                     string
			shift, width, notSupported uint32
		}{
			{nvlink.SignalFabricBandwidthDegraded, gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW, gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW, gonvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_NOT_SUPPORTED},
			{nvlink.SignalFabricRouteRecoveryInProgress, gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY, gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_NOT_SUPPORTED},
			{nvlink.SignalFabricRouteUnhealthy, gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY, gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY, gonvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_NOT_SUPPORTED},
			{nvlink.SignalFabricAccessTimeoutRecovery, gonvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY, gonvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY, gonvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_NOT_SUPPORTED},
		} {
			if field(bit.shift, bit.width) == bit.notSupported {
				m.MarkUnsupported(bit.signal)
			}
		}
		m.FabricRegistrationIncomplete = info.State != gonvml.GPU_FABRIC_STATE_COMPLETED
		m.FabricRegistrationFailed = info.State == gonvml.GPU_FABRIC_STATE_COMPLETED &&
			gonvml.Return(info.Status) != gonvml.SUCCESS
	case gonvml.ERROR_NOT_SUPPORTED:
		// On GB200/GB300 fabric registration is mandatory, so a not-supported
		// result is flagged as unregistered rather than skipped. That is a
		// value, so only the health conditions are marked unsupported.
		m.FabricRegistrationIncomplete = true
		m.MarkUnsupported(
			nvlink.SignalFabricBandwidthDegraded,
			nvlink.SignalFabricRouteRecoveryInProgress,
			nvlink.SignalFabricRouteUnhealthy,
			nvlink.SignalFabricAccessTimeoutRecovery,
		)
		slog.Debug("nvml: fabric info not supported on this device", "gpu", m.GPUID)
	default:
		m.AddError(&Error{Op: "DeviceGetGpuFabricInfoV", Return: ret},
			nvlink.SignalFabricBandwidthDegraded,
			nvlink.SignalFabricRouteRecoveryInProgress,
			nvlink.SignalFabricRouteUnhealthy,
			nvlink.SignalFabricAccessTimeoutRecovery,
			nvlink.SignalFabricRegistrationIncomplete,
			nvlink.SignalFabricRegistrationFailed)
	}
}

// measureDeviceLinks sets the per-GPU NVLink link-count and per-link
// speed signals on m from this device's field values.
//
// Signal mapping (both from DeviceGetFieldValues):
//   - link_count           ← FI_DEV_NVLINK_LINK_COUNT
//   - speed_mbytes_per_sec ← FI_DEV_NVLINK_SPEED_MBPS_COMMON
//
// NVML's MBPS field is MegaBytes per second despite the name, so 50000
// equals 50 GB/s (matching nvidia-smi). The fields are documented to
// return VALUE_TYPE_UNSIGNED_INT, verified before the 4-byte decode so a
// future wider type becomes a per-signal failure instead of a silent
// truncation. A failed call flags both signals; a per-entry failure
// flags only the affected one. When the common speed field returns
// ERROR_NOT_SUPPORTED — some drivers (e.g. R580) do not expose it even on
// fully-trained NVLink — scoped NVLINK_GET_SPEED fields are tried as a
// fallback; if those are also unavailable the speed is flagged as a
// measurement failure, which fails the dependent check Critical.
func measureDeviceLinks(dev gonvml.Device, m *nvlink.NVLinkMeasurement) {
	values := []gonvml.FieldValue{
		{FieldId: gonvml.FI_DEV_NVLINK_SPEED_MBPS_COMMON},
		{FieldId: gonvml.FI_DEV_NVLINK_LINK_COUNT},
	}
	if ret := dev.GetFieldValues(values); ret != gonvml.SUCCESS {
		m.AddError(&Error{Op: "DeviceGetFieldValues", Return: ret},
			nvlink.SignalSpeedMBytesPerSec, nvlink.SignalLinkCount)
		return
	}

	// Link count is decoded before the speed: the per-link speed fallback
	// needs it to know how many scopes are real links that must report.
	switch countRet := gonvml.Return(values[1].NvmlReturn); countRet {
	case gonvml.SUCCESS:
		if gonvml.ValueType(values[1].ValueType) != gonvml.VALUE_TYPE_UNSIGNED_INT {
			m.AddError(fmt.Errorf("nvml DeviceGetFieldValues(NVLINK_LINK_COUNT): unexpected ValueType %d, want UNSIGNED_INT", values[1].ValueType),
				nvlink.SignalLinkCount)
		} else {
			m.LinkCount = int64(binary.LittleEndian.Uint32(values[1].Value[:4]))
		}
	default:
		m.AddError(&Error{Op: "DeviceGetFieldValues(NVLINK_LINK_COUNT)", Return: countRet},
			nvlink.SignalLinkCount)
	}

	switch speedRet := gonvml.Return(values[0].NvmlReturn); speedRet {
	case gonvml.SUCCESS:
		if gonvml.ValueType(values[0].ValueType) != gonvml.VALUE_TYPE_UNSIGNED_INT {
			m.AddError(fmt.Errorf("nvml DeviceGetFieldValues(NVLINK_SPEED_MBPS_COMMON): unexpected ValueType %d, want UNSIGNED_INT", values[0].ValueType),
				nvlink.SignalSpeedMBytesPerSec)
		} else {
			m.SpeedMBytesPerSec = int64(binary.LittleEndian.Uint32(values[0].Value[:4]))
		}
	case gonvml.ERROR_NOT_SUPPORTED:
		// Some drivers (e.g. R580) report the common speed field as Not
		// Supported even on healthy NVLink; fall back to the scoped per-link
		// fields. If those are also unavailable it is a measurement failure.
		if speed, err := deviceMinLinkSpeed(dev, m.LinkCount); err != nil {
			m.AddError(err, nvlink.SignalSpeedMBytesPerSec)
		} else {
			m.SpeedMBytesPerSec = speed
		}
	default:
		m.AddError(&Error{Op: "DeviceGetFieldValues(NVLINK_SPEED_MBPS_COMMON)", Return: speedRet},
			nvlink.SignalSpeedMBytesPerSec)
	}
}

// deviceMinLinkSpeed reads the per-link NVLink speed via FI_DEV_NVLINK_GET_SPEED
// (which carries the link index in ScopeId; the legacy per-link field IDs are
// unavailable on Hopper and newer GPUs) and returns the slowest link's speed.
// Returning the minimum rather than the maximum is deliberate: a single
// degraded link is the failure this check exists to catch, and a maximum would
// hide it behind healthy peers.
//
// linkCount bounds which scopes are real links. Every scope below it must
// return a usable positive speed, so a NOT_SUPPORTED, INVALID_ARGUMENT, zero,
// or unexpected value type there is a measurement failure (a non-nil error),
// not something to skip past. Scopes at or above linkCount are beyond the GPU's
// lane count and are ignored. A hard NVML failure on any scope is also an
// error. The caller turns any error into a Critical verdict.
func deviceMinLinkSpeed(dev gonvml.Device, linkCount int64) (int64, error) {
	values := make([]gonvml.FieldValue, gonvml.NVLINK_MAX_LINKS)
	for link := range values {
		values[link].FieldId = gonvml.FI_DEV_NVLINK_GET_SPEED
		values[link].ScopeId = uint32(link)
	}
	if ret := dev.GetFieldValues(values); ret != gonvml.SUCCESS {
		return 0, &Error{Op: "DeviceGetFieldValues(NVLINK_GET_SPEED)", Return: ret}
	}

	var minSpeed int64
	seen := 0
	for i := range values {
		inRange := int64(i) < linkCount
		switch entryRet := gonvml.Return(values[i].NvmlReturn); entryRet {
		case gonvml.SUCCESS:
			if !inRange {
				continue // a scope beyond the GPU's lane count; ignore.
			}
			if gonvml.ValueType(values[i].ValueType) != gonvml.VALUE_TYPE_UNSIGNED_INT {
				return 0, fmt.Errorf("nvml DeviceGetFieldValues(NVLINK_GET_SPEED) link %d: unexpected ValueType %d, want UNSIGNED_INT", i, values[i].ValueType)
			}
			v := int64(binary.LittleEndian.Uint32(values[i].Value[:4]))
			if v <= 0 {
				return 0, fmt.Errorf("nvml DeviceGetFieldValues(NVLINK_GET_SPEED) link %d: reported no speed", i)
			}
			if seen == 0 || v < minSpeed {
				minSpeed = v
			}
			seen++
		case gonvml.ERROR_NOT_SUPPORTED, gonvml.ERROR_INVALID_ARGUMENT:
			if inRange {
				return 0, &Error{Op: fmt.Sprintf("DeviceGetFieldValues(NVLINK_GET_SPEED) link %d", i), Return: entryRet}
			}
			// Scope beyond the GPU's lane count; ignore.
		default:
			return 0, &Error{Op: fmt.Sprintf("DeviceGetFieldValues(NVLINK_GET_SPEED) link %d", i), Return: entryRet}
		}
	}
	if seen == 0 {
		return 0, fmt.Errorf("nvml DeviceGetFieldValues(NVLINK_GET_SPEED): no in-range link reported a speed")
	}
	return minSpeed, nil
}

// measureDeviceActiveLinks counts this device's inactive
// (FEATURE_DISABLED) NVLinks onto m.InactiveCount, attributing a probe
// failure to inactive_count alone.
//
// Per-link returns: ERROR_INVALID_ARGUMENT is a link index above the
// GPU's lane count and ERROR_NOT_SUPPORTED means the device has no
// NVLink — both are shape-of-hardware, not failures.
// Any other return is an observation failure; the signal is flagged once
// and probing stops so a single broken library does not stack identical
// entries. On break the partial count is still recorded, but the errored
// signal drives the verdict Critical regardless.
func measureDeviceActiveLinks(dev gonvml.Device, m *nvlink.NVLinkMeasurement) {
	var inactive int64
linkLoop:
	for link := 0; link < gonvml.NVLINK_MAX_LINKS; link++ {
		state, ret := dev.GetNvLinkState(link)
		switch ret {
		case gonvml.SUCCESS:
			if state == gonvml.FEATURE_DISABLED {
				inactive++
			}
		case gonvml.ERROR_INVALID_ARGUMENT:
			// Link index above this GPU's physical lane count.
		case gonvml.ERROR_NOT_SUPPORTED:
			// Device exposes no NVLink at all; no further links to probe.
			break linkLoop
		default:
			m.AddError(&Error{Op: "DeviceGetNvLinkState", Return: ret},
				nvlink.SignalInactiveCount)
			break linkLoop
		}
	}
	m.InactiveCount = inactive
}

// measureDeviceLinkCapabilities sets the per-GPU NVLink capability
// signals on m. Each signal is true when every NVLink the GPU exposes
// reports that capability as supported, and false as soon as any present
// link reports it unsupported.
//
// Capability mapping (each from DeviceGetNvLinkCapability per link):
//   - all_links_support_p2p            ← NVLINK_CAP_P2P_SUPPORTED
//   - all_links_support_sysmem_access  ← NVLINK_CAP_SYSMEM_ACCESS
//   - all_links_support_p2p_atomics    ← NVLINK_CAP_P2P_ATOMICS
//   - all_links_support_sysmem_atomics ← NVLINK_CAP_SYSMEM_ATOMICS
//   - all_links_support_sli            ← NVLINK_CAP_SLI_BRIDGE
//   - all_links_support_link           ← NVLINK_CAP_VALID
//
// Present links are enumerated with GetNvLinkState.
// ERROR_INVALID_ARGUMENT is a link index above the GPU's lane count (skip).
// Any other return, including ERROR_NOT_SUPPORTED, means the state query
// failed; it flags every unresolved capability signal.
// A per-capability query failure flags that capability signal; each is flagged
// at most once so a misbehaving link does not stack duplicate entries across
// link indices.
func measureDeviceLinkCapabilities(dev gonvml.Device, m *nvlink.NVLinkMeasurement) {
	m.AllLinksSupportP2P = true
	m.AllLinksSupportSysmemAccess = true
	m.AllLinksSupportP2PAtomics = true
	m.AllLinksSupportSysmemAtomics = true
	m.AllLinksSupportSLI = true
	m.AllLinksSupportLink = true

	caps := []struct {
		field      *bool
		signal     string
		capability gonvml.NvLinkCapability
	}{
		{field: &m.AllLinksSupportP2P, signal: nvlink.SignalAllLinksSupportP2P, capability: gonvml.NVLINK_CAP_P2P_SUPPORTED},
		{field: &m.AllLinksSupportSysmemAccess, signal: nvlink.SignalAllLinksSupportSysmemAccess, capability: gonvml.NVLINK_CAP_SYSMEM_ACCESS},
		{field: &m.AllLinksSupportP2PAtomics, signal: nvlink.SignalAllLinksSupportP2PAtomics, capability: gonvml.NVLINK_CAP_P2P_ATOMICS},
		{field: &m.AllLinksSupportSysmemAtomics, signal: nvlink.SignalAllLinksSupportSysmemAtomics, capability: gonvml.NVLINK_CAP_SYSMEM_ATOMICS},
		{field: &m.AllLinksSupportSLI, signal: nvlink.SignalAllLinksSupportSLI, capability: gonvml.NVLINK_CAP_SLI_BRIDGE},
		{field: &m.AllLinksSupportLink, signal: nvlink.SignalAllLinksSupportLink, capability: gonvml.NVLINK_CAP_VALID},
	}

	// Track which capability signals are already resolved — either flagged
	// errored or determined unsupported — so we stop probing them.
	resolved := make([]bool, len(caps))

linkLoop:
	for link := 0; link < gonvml.NVLINK_MAX_LINKS; link++ {
		_, ret := dev.GetNvLinkState(link)
		switch ret {
		case gonvml.SUCCESS:
			// Probe this link's capabilities below.
		case gonvml.ERROR_INVALID_ARGUMENT:
			continue // link index above this GPU's physical lane count
		default:
			// The link roster itself is unreadable, so no capability
			// signal is trustworthy for this GPU. Record one shared
			// entry covering every signal not already resolved.
			var failed []string
			for i := range caps {
				if !resolved[i] {
					failed = append(failed, caps[i].signal)
				}
			}
			if len(failed) > 0 {
				m.AddError(&Error{Op: "DeviceGetNvLinkState", Return: ret}, failed...)
			}
			break linkLoop
		}

		for i := range caps {
			if resolved[i] {
				continue
			}
			supported, capRet := dev.GetNvLinkCapability(link, caps[i].capability)
			if capRet != gonvml.SUCCESS {
				m.AddError(&Error{Op: "DeviceGetNvLinkCapability", Return: capRet}, caps[i].signal)
				resolved[i] = true
				continue
			}
			if supported == 0 {
				*caps[i].field = false
				resolved[i] = true
			}
		}
	}
}

// architectureUnknown is reported when a device name reads but none maps to
// a recognized architecture; see [gpuinventory.ArchitectureUnknown].
const architectureUnknown = gpuinventory.ArchitectureUnknown

// Architecture reports the node's GPU architecture as one of the catalog
// architecture identifiers (e.g. "GB200", "GB300", "H100"), derived from the
// NVML product name of the first enumerated device whose name reads.
//
// [architectureUnknown] is returned only when at least one device name reads
// but none maps to a known architecture — a healthy but unrecognized GPU that
// architecture-scoped checks then safely skip. A discovery *failure* is an
// error, not "unknown": an enumeration failure (DeviceGetCount) propagates,
// zero enumerated devices is an error, and a node whose every device slot is
// unreadable (e.g. all GPUs lost) is an error. Callers must treat that error
// as a node fault — draining explicitly — rather than run with filtering
// disabled, which would evaluate every check against the wrong thresholds or
// skip a dead node into a false PASS.
func (b *NVMLBackend) Architecture() (string, error) {
	devs, err := b.devices()
	if err != nil {
		return "", err
	}
	if len(devs) == 0 {
		return "", fmt.Errorf("nvml architecture discovery: no GPUs enumerated")
	}

	nameRead := false
	var firstFailure error
	for _, d := range devs {
		if ed, errored := d.(erroredDevice); errored {
			if firstFailure == nil {
				firstFailure = &Error{Op: "DeviceGetHandleByIndex", Return: ed.ret}
			}
			continue
		}
		name, ret := d.GetName()
		if ret != gonvml.SUCCESS {
			if firstFailure == nil {
				firstFailure = &Error{Op: "DeviceGetName", Return: ret}
			}
			continue
		}
		nameRead = true
		if arch := architectureFromName(name); arch != "" {
			return arch, nil
		}
	}
	if !nameRead {
		return "", fmt.Errorf("nvml architecture discovery: no GPU name could be read from %d device(s): %w", len(devs), firstFailure)
	}
	return architectureUnknown, nil
}

// architectureFromName is shared with the collect inventory path so both
// discover the same architecture from the same name.
func architectureFromName(name string) string { return gpuinventory.ArchitectureFromName(name) }
