package nvml

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

func telemetrySignals() collect.Signals {
	return collect.Signals{
		gpu.FramebufferTotal, gpu.FramebufferFree, gpu.FramebufferUsed,
		gpu.FramebufferReserved, gpu.FramebufferUsedRatio, gpu.UnifiedMemory,
		gpu.ComputeUtilization, gpu.MemoryCopyUtilization, gpu.GraphicsClock, gpu.SMClock,
		gpu.MemoryClock, gpu.PerformanceState, gpu.ClockEventReasons,
		gpu.HardwareSlowdownActive, gpu.HardwareThermalSlowdownActive,
		gpu.HardwarePowerBrakeSlowdownActive, gpu.PersistenceMode, gpu.MinorNumber,
	}
}

func telemetryValue(batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, signal collect.Signal, value *observation.Value) {
	batch.Observations = append(batch.Observations, &observation.Observation{
		SignalId: signal.ID(), Unit: signal.Unit(), Source: source, Target: target, ObservedAt: now,
		Outcome: &observation.Observation_Value{Value: value},
	})
}

func telemetryUint(v uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: v}}
}

func telemetryDouble(v float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: v}}
}

func telemetryBool(v bool) *observation.Value {
	return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}}
}

// readFramebuffer falls back only for absent or unsupported v2 APIs. A permission
// failure or lost GPU must not be hidden by a successful older query.
func readFramebuffer(dev device.Device) (nvml.Memory_v2, bool, nvml.Return) {
	info, ret := dev.GetMemoryInfo_v2()
	if ret != nvml.ERROR_NOT_SUPPORTED && ret != nvml.ERROR_FUNCTION_NOT_FOUND && ret != nvml.ERROR_ARGUMENT_VERSION_MISMATCH {
		return info, true, ret
	}
	old, ret := dev.GetMemoryInfo()
	return nvml.Memory_v2{Total: old.Total, Free: old.Free, Used: old.Used}, false, ret
}

// readUnifiedMemory uses available memory, including reclaimable cache, rather
// than treating every cached page as allocated GPU memory.
func readUnifiedMemory(fsys fs.FS) (nvml.Memory_v2, error) {
	data, err := fs.ReadFile(fsys, "proc/meminfo")
	if err != nil {
		return nvml.Memory_v2{}, err
	}
	values := make(map[string]uint64, 3)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:" && fields[0] != "MemFree:") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return nvml.Memory_v2{}, fmt.Errorf("invalid meminfo field %q", line)
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || v > ^uint64(0)/1024 {
			return nvml.Memory_v2{}, fmt.Errorf("invalid meminfo quantity %q", line)
		}
		if _, duplicate := values[fields[0]]; duplicate {
			return nvml.Memory_v2{}, fmt.Errorf("duplicate meminfo field %s", fields[0])
		}
		values[fields[0]] = v * 1024
	}
	total, hasTotal := values["MemTotal:"]
	available, hasAvailable := values["MemAvailable:"]
	free, hasFree := values["MemFree:"]
	if !hasTotal || !hasAvailable || !hasFree || total == 0 || available > total || free > total {
		return nvml.Memory_v2{}, fmt.Errorf("invalid or missing MemTotal/MemAvailable/MemFree")
	}
	return nvml.Memory_v2{Total: total, Free: free, Used: total - available}, nil
}

func measureTelemetry(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) {
	measureTelemetryFS(ctx, batch, now, target, dev, want, nil)
}

func measureTelemetryFS(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool, fsys fs.FS) {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	unreadable := nvml.SUCCESS
	start := len(batch.Observations)
	defer func() {
		if unreadable == nvml.SUCCESS {
			return
		}
		collected := make(map[string]bool)
		for _, current := range batch.Observations[start:] {
			collected[current.SignalId] = true
		}
		for _, signal := range telemetrySignals() {
			if want[signal.ID()] && !collected[signal.ID()] {
				batch.Observations = append(batch.Observations, errorObservation(now, target, signal, "Telemetry", observation.CollectionErrorCategoryUnavailable, returnName(unreadable), nvml.ErrorString(unreadable)))
			}
		}
	}()
	failed := func(signal collect.Signal, operation string, ret nvml.Return) {
		if isGPULost(ret) || isGPURequiresReset(ret) {
			unreadable = ret
		}
		batch.Observations = append(batch.Observations, errorObservation(now, target, signal, operation, categorizeFailure(signal, ret), returnName(ret), nvml.ErrorString(ret)))
	}
	memorySignals := collect.Signals{gpu.FramebufferTotal, gpu.FramebufferFree, gpu.FramebufferUsed, gpu.FramebufferReserved, gpu.FramebufferUsedRatio, gpu.UnifiedMemory}
	memoryWanted := false
	for _, signal := range memorySignals {
		memoryWanted = memoryWanted || want[signal.ID()]
	}
	if memoryWanted && ctx.Err() == nil {
		info, reservedKnown, ret := readFramebuffer(dev)
		unified := false
		var hostErr error
		if ret == nvml.ERROR_NOT_SUPPORTED {
			// Coherent ATS access is the capability classification available in
			// nvlib. Require it in addition to unavailable framebuffer accounting.
			coherent, err := dev.IsCoherent()
			if err != nil {
				hostErr = err
			} else if coherent {
				unified = true
				info, hostErr = readUnifiedMemory(fsys)
				if hostErr == nil {
					ret = nvml.SUCCESS
				}
			}
		}
		for _, signal := range memorySignals {
			if !want[signal.ID()] {
				continue
			}
			if hostErr != nil {
				category := observation.CollectionErrorCategoryParseError
				if _, ok := hostErr.(*fs.PathError); ok {
					category = observation.CollectionErrorCategorySourceError
				}
				if os.IsPermission(hostErr) {
					category = observation.CollectionErrorCategoryPermissionDenied
				}
				batch.Observations = append(batch.Observations, errorObservation(now, target, signal, "ReadUnifiedMemory", category, "", hostErr.Error()))
				continue
			}
			if ret != nvml.SUCCESS {
				failed(signal, "GetMemoryInfo", ret)
				continue
			}
			if info.Free > info.Total || info.Used > info.Total || (reservedKnown && info.Reserved > info.Total) {
				batch.Observations = append(batch.Observations, errorObservation(now, target, signal, "GetMemoryInfo", observation.CollectionErrorCategoryParseError, "", "memory quantity exceeds total memory"))
				continue
			}
			if signal == gpu.FramebufferReserved && (!reservedKnown || unified) {
				failed(signal, "GetMemoryInfo_v2", nvml.ERROR_NOT_SUPPORTED)
				continue
			}
			var value *observation.Value
			switch signal {
			case gpu.FramebufferTotal:
				value = telemetryDouble(float64(info.Total) / (1024 * 1024))
			case gpu.FramebufferFree:
				value = telemetryDouble(float64(info.Free) / (1024 * 1024))
			case gpu.FramebufferUsed:
				value = telemetryDouble(float64(info.Used) / (1024 * 1024))
			case gpu.FramebufferReserved:
				value = telemetryDouble(float64(info.Reserved) / (1024 * 1024))
			case gpu.FramebufferUsedRatio:
				if info.Total == 0 {
					batch.Observations = append(batch.Observations, errorObservation(now, target, signal, "GetMemoryInfo", observation.CollectionErrorCategoryParseError, "", "total memory is zero; ratio is undefined"))
					continue
				}
				value = telemetryDouble(float64(info.Used) / float64(info.Total))
			case gpu.UnifiedMemory:
				value = telemetryBool(unified)
			}
			telemetryValue(batch, now, target, signal, value)
		}
	}
	if unreadable == nvml.SUCCESS && ctx.Err() == nil && (want[gpu.ComputeUtilization.ID()] || want[gpu.MemoryCopyUtilization.ID()]) {
		util, ret := dev.GetUtilizationRates()
		for _, read := range []struct {
			signal collect.Signal
			value  uint32
		}{{signal: gpu.ComputeUtilization, value: util.Gpu}, {signal: gpu.MemoryCopyUtilization, value: util.Memory}} {
			if !want[read.signal.ID()] {
				continue
			}
			if ret != nvml.SUCCESS {
				failed(read.signal, "GetUtilizationRates", ret)
			} else if read.value > 100 {
				batch.Observations = append(batch.Observations, errorObservation(now, target, read.signal, "GetUtilizationRates", observation.CollectionErrorCategoryParseError, "", "utilization exceeds 100 percent"))
			} else {
				telemetryValue(batch, now, target, read.signal, telemetryUint(uint64(read.value)))
			}
		}
	}
	for _, clock := range []struct {
		signal collect.Signal
		kind   nvml.ClockType
	}{{signal: gpu.GraphicsClock, kind: nvml.CLOCK_GRAPHICS}, {signal: gpu.SMClock, kind: nvml.CLOCK_SM}, {signal: gpu.MemoryClock, kind: nvml.CLOCK_MEM}} {
		if unreadable != nvml.SUCCESS || ctx.Err() != nil {
			return
		}
		if !want[clock.signal.ID()] {
			continue
		}
		value, ret := dev.GetClockInfo(clock.kind)
		if ret != nvml.SUCCESS {
			failed(clock.signal, "GetClockInfo", ret)
		} else if value > math.MaxInt32 {
			batch.Observations = append(batch.Observations, errorObservation(now, target, clock.signal, "GetClockInfo", observation.CollectionErrorCategoryParseError, "", "invalid clock value"))
		} else {
			telemetryValue(batch, now, target, clock.signal, telemetryUint(uint64(value)))
		}
	}
	if unreadable == nvml.SUCCESS && ctx.Err() == nil && want[gpu.PerformanceState.ID()] {
		value, ret := dev.GetPerformanceState()
		if ret != nvml.SUCCESS {
			failed(gpu.PerformanceState, "GetPerformanceState", ret)
		} else if value < nvml.PSTATE_0 || value > nvml.PSTATE_15 {
			batch.Observations = append(batch.Observations, errorObservation(now, target, gpu.PerformanceState, "GetPerformanceState", observation.CollectionErrorCategoryParseError, "", "invalid performance state"))
		} else {
			telemetryValue(batch, now, target, gpu.PerformanceState, telemetryUint(uint64(value)))
		}
	}
	reasonSignals := collect.Signals{gpu.ClockEventReasons, gpu.HardwareSlowdownActive, gpu.HardwareThermalSlowdownActive, gpu.HardwarePowerBrakeSlowdownActive}
	reasonsWanted := false
	for _, signal := range reasonSignals {
		reasonsWanted = reasonsWanted || want[signal.ID()]
	}
	if unreadable == nvml.SUCCESS && ctx.Err() == nil && reasonsWanted {
		value, ret := dev.GetCurrentClocksEventReasons()
		if ret == nvml.ERROR_FUNCTION_NOT_FOUND {
			value, ret = dev.GetCurrentClocksThrottleReasons()
		}
		for _, signal := range reasonSignals {
			if !want[signal.ID()] {
				continue
			}
			if ret != nvml.SUCCESS {
				failed(signal, "GetCurrentClocksEventReasons", ret)
				continue
			}
			result := telemetryUint(value)
			switch signal {
			case gpu.HardwareSlowdownActive:
				result = telemetryBool(value&nvml.ClocksThrottleReasonHwSlowdown != 0)
			case gpu.HardwareThermalSlowdownActive:
				result = telemetryBool(value&nvml.ClocksThrottleReasonHwThermalSlowdown != 0)
			case gpu.HardwarePowerBrakeSlowdownActive:
				result = telemetryBool(value&nvml.ClocksThrottleReasonHwPowerBrakeSlowdown != 0)
			}
			telemetryValue(batch, now, target, signal, result)
		}
	}
	if unreadable == nvml.SUCCESS && ctx.Err() == nil && want[gpu.PersistenceMode.ID()] {
		value, ret := dev.GetPersistenceMode()
		if ret != nvml.SUCCESS {
			failed(gpu.PersistenceMode, "GetPersistenceMode", ret)
		} else {
			telemetryValue(batch, now, target, gpu.PersistenceMode, telemetryUint(uint64(value)))
		}
	}
	if unreadable == nvml.SUCCESS && ctx.Err() == nil && want[gpu.MinorNumber.ID()] {
		value, ret := dev.GetMinorNumber()
		if ret != nvml.SUCCESS {
			failed(gpu.MinorNumber, "GetMinorNumber", ret)
		} else if value < 0 {
			failed(gpu.MinorNumber, "GetMinorNumber", nvml.ERROR_NO_DATA)
		} else {
			telemetryValue(batch, now, target, gpu.MinorNumber, telemetryUint(uint64(value)))
		}
	}
}
