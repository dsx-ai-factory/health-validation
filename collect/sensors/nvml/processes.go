// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	processsource "github.com/dsx-ai-factory/health-validation/collect/source/process"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func processSignals() collect.Signals           { return gpusignals.Processes() }
func processDiagnosticSignals() collect.Signals { return gpusignals.ProcessDiagnostics() }

// measureProcesses runs inside the sensor's native-call gate. Filesystem paths
// are relative to the host root. Use measureProcessesFS to inject a host root.
func measureProcesses(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) {
	measureProcessesFS(ctx, batch, now, target, dev, want, nil)
}

func measureProcessesFS(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool, fsys fs.FS) {
	measureProcessesWithDiagnostics(ctx, batch, now, target, dev, want, fsys, false)
}

func measureProcessesWithDiagnostics(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool, fsys fs.FS, cmdline bool) {
	var selected collect.Signals
	needIdentity := false
	declared := processSignals()
	if cmdline {
		declared = append(declared, processDiagnosticSignals()...)
	}
	for _, definition := range declared {
		if want[definition.ID()] {
			selected = append(selected, definition)
			needIdentity = needIdentity || definition != gpusignals.ProcessCount
		}
	}
	if len(selected) == 0 || ctx.Err() != nil {
		return
	}
	reader := processsource.New(fsys)
	var bootID string
	var identityErr error
	before := make(map[uint32]processsource.Stat)
	beforeErrors := make(map[uint32]error)
	if needIdentity {
		bootID, identityErr = reader.BootID()
		if identityErr == nil {
			var pids []uint32
			pids, identityErr = reader.PIDs()
			for _, pid := range pids {
				if ctx.Err() != nil {
					return
				}
				stat, err := reader.Stat(pid)
				if err == nil {
					before[pid] = stat
				} else {
					beforeErrors[pid] = err
				}
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	members, ret := dev.GetComputeRunningProcesses()
	if ret != nvml.SUCCESS {
		for _, definition := range selected {
			batch.Observations = append(batch.Observations, errorObservation(now, target, definition, "GetComputeRunningProcesses", categorizeFailure(definition, ret), returnName(ret), nvml.ErrorString(ret)))
		}
		return
	}
	// Count is native membership, independent of host metadata availability.
	if want[gpusignals.ProcessCount.ID()] {
		appendProcessValue(batch, now, target, gpusignals.ProcessCount, processUint(uint64(len(members))))
	}
	if len(members) == 0 {
		for _, definition := range selected {
			if definition != gpusignals.ProcessCount {
				batch.Observations = append(batch.Observations, observation.NewNoValueObservation(definition.ID(), definition.Unit(), source, target, now, &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "No compute processes reported by NVML"}))
			}
		}
		return
	}
	if !needIdentity {
		return
	}
	var missing []string
	var missingErr error
	for _, member := range members {
		if ctx.Err() != nil {
			return
		}
		stat, err := reader.Stat(member.Pid)
		if identityErr != nil {
			err = identityErr
		}
		if err == nil {
			previous, found := before[member.Pid]
			if !found {
				err = beforeErrors[member.Pid]
				if err == nil {
					err = fmt.Errorf("PID %d was not present before native membership read", member.Pid)
				}
			} else if previous.StartTimeTicks != stat.StartTimeTicks {
				err = fmt.Errorf("PID %d reused during native membership read", member.Pid)
			}
		}
		if err != nil {
			missing = append(missing, fmt.Sprintf("PID %d: %v", member.Pid, err))
			if missingErr == nil || errors.Is(err, fs.ErrPermission) {
				missingErr = err
			}
			continue
		}
		processTarget := observation.GPUProcessTarget(target.GetGpu().GetUuid(), bootID, member.Pid, stat.StartTimeTicks)
		if want[gpusignals.ProcessMember.ID()] {
			appendProcessValue(batch, now, processTarget, gpusignals.ProcessMember, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: true}})
		}
		if want[gpusignals.ProcessMemoryUsed.ID()] {
			if member.UsedGpuMemory == ^uint64(0) {
				batch.Observations = append(batch.Observations, errorObservation(now, processTarget, gpusignals.ProcessMemoryUsed, "GetComputeRunningProcesses", observation.CollectionErrorCategoryUnavailable, "NVML_VALUE_NOT_AVAILABLE", "NVML did not report process GPU memory"))
			} else {
				appendProcessValue(batch, now, processTarget, gpusignals.ProcessMemoryUsed, processUint(member.UsedGpuMemory))
			}
		}
		if want[gpusignals.ProcessState.ID()] {
			appendProcessValue(batch, now, processTarget, gpusignals.ProcessState, &observation.Value{Kind: &observation.Value_StringValue{StringValue: stat.State}})
		}
		if want[gpusignals.ProcessName.ID()] {
			name, err := reader.Comm(member.Pid)
			if err == nil {
				after, readErr := reader.Stat(member.Pid)
				err = readErr
				if err == nil && after.StartTimeTicks != stat.StartTimeTicks {
					err = fmt.Errorf("PID %d reused during comm read", member.Pid)
				}
			}
			if err != nil {
				batch.Observations = append(batch.Observations, processReadFailure(now, processTarget, gpusignals.ProcessName, err))
			} else {
				appendProcessValue(batch, now, processTarget, gpusignals.ProcessName, &observation.Value{Kind: &observation.Value_StringValue{StringValue: name}})
			}
		}
		if cmdline && (want[gpusignals.ProcessCmdline.ID()] || want[gpusignals.ProcessCmdlineTruncated.ID()]) {
			measureProcessCmdline(ctx, batch, now, processTarget, reader, stat, want)
		}
	}
	if missingErr != nil {
		for _, definition := range selected {
			if definition == gpusignals.ProcessCount {
				continue
			}
			failure := processReadFailure(now, target, definition, missingErr)
			failure.GetCollectionError().Detail = strings.Join(missing, "; ")
			batch.Observations = append(batch.Observations, failure)
		}
	}
}

func processUint(value uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}
}

func appendProcessValue(batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, definition collect.Signal, value *observation.Value) {
	batch.Observations = append(batch.Observations, &observation.Observation{SignalId: definition.ID(), Unit: definition.Unit(), Source: source, Target: target, ObservedAt: now, Outcome: &observation.Observation_Value{Value: value}})
}

func processReadFailure(now *timestamppb.Timestamp, target *observation.ObservationTarget, definition collect.Signal, err error) *observation.Observation {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, processsource.ErrMalformed):
		category = observation.CollectionErrorCategoryParseError
	case errors.Is(err, fs.ErrNotExist):
		category = observation.CollectionErrorCategoryUnavailable
	}
	return errorObservation(now, target, definition, "procfs process identity", category, "", err.Error())
}

func measureProcessCmdline(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, reader *processsource.Reader, stat processsource.Stat, want map[string]bool) {
	if ctx.Err() != nil {
		return
	}
	current, err := reader.Stat(stat.PID)
	if err == nil && current.StartTimeTicks != stat.StartTimeTicks {
		err = fmt.Errorf("PID %d reused before cmdline read", stat.PID)
	}
	blocked := err == nil && (stat.State == "D" || current.State == "D")
	if blocked {
		err = fmt.Errorf("cmdline read skipped for PID %d in D-state", stat.PID)
	}
	var text string
	var truncated bool
	if err == nil {
		text, truncated, err = reader.Cmdline(stat.PID, 4096)
		if err == nil {
			after, readErr := reader.Stat(stat.PID)
			err = readErr
			if err == nil && after.StartTimeTicks != stat.StartTimeTicks {
				err = fmt.Errorf("PID %d reused during cmdline read", stat.PID)
			}
		}
	}
	for _, definition := range processDiagnosticSignals() {
		if !want[definition.ID()] {
			continue
		}
		if err != nil {
			failure := processReadFailure(now, target, definition, err)
			failure.GetCollectionError().Operation = "ReadCmdline"
			if blocked {
				failure.GetCollectionError().Category = observation.CollectionErrorCategoryUnavailable
			}
			batch.Observations = append(batch.Observations, failure)
			continue
		}
		if definition == gpusignals.ProcessCmdline {
			appendProcessValue(batch, now, target, definition, &observation.Value{Kind: &observation.Value_StringValue{StringValue: text}})
		} else {
			appendProcessValue(batch, now, target, definition, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: truncated}})
		}
	}
}
