// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

const (
	// procCmdlineMax caps one cmdline, as in v0.3.
	procCmdlineMax = 250
	// opProcessIdentity is the collect NVML sensor's operation for a compute
	// process it could not identify in procfs.
	opProcessIdentity = "procfs process identity"
)

// gpuProcesses collects, per GPU UUID, the gpu.process.cmdline observations
// that fold into compute_process_cmdlines. Observations of a GPU without a
// UUID stay with the field table, which fails them.
type gpuProcesses map[string][]*observation.Observation

func (p gpuProcesses) add(current *observation.Observation) bool {
	if current.GetSignalId() != gpusignals.ProcessCmdline.ID() {
		return false
	}
	uuid := gpuRef(current.GetTarget()).GetUuid()
	if uuid == "" {
		return false
	}
	p[uuid] = append(p[uuid], current)
	return true
}

func (p gpuProcesses) feed(b *builder) {
	s := schemaFor(gpu.GPUMeasurement{})
	for uuid, obs := range p {
		text, err := computeProcessCmdlines(obs)
		b.entity(s, health.VerifiedEntity(uuid), uuid).setAggregate(s, gpu.SignalComputeProcessCmdlines, text, err)
	}
}

// computeProcessCmdlines formats one line per compute process, as in v0.3:
// pid=N, then the quoted cmdline when it was read. The
// text is diagnostic, since gpu_compute_apps fails on the process count, so a
// cmdline or process identity that could not be read only drops its text.
// Any other GPU-level failure, such as NVML failing to list the processes,
// fails the field as it fails the count.
func computeProcessCmdlines(obs []*observation.Observation) (string, error) {
	var lines []string
	var none bool
	seen := make(map[uint32]bool)
	for _, o := range obs {
		process := o.GetTarget().GetGpuProcess()
		if process == nil {
			switch {
			case o.GetNoValue().GetReason() == observation.NoValueReasonNoTargets:
				none = true
			case o.GetCollectionError().GetOperation() == opProcessIdentity:
			default:
				return "", outcomeFailure(o)
			}
			continue
		}
		pid := process.GetProcess().GetPid()
		if seen[pid] {
			return "", fmt.Errorf("signal %q was observed more than once for PID %d", o.GetSignalId(), pid)
		}
		seen[pid] = true
		line := fmt.Sprintf("pid=%d", pid)
		if o.GetValue() != nil {
			text, ok := o.GetValue().GetKind().(*observation.Value_StringValue)
			if !ok {
				return "", fmt.Errorf("signal %q value %v is not a string", o.GetSignalId(), o.GetValue())
			}
			if cmdline := text.StringValue; cmdline != "" {
				if len(cmdline) > procCmdlineMax {
					cmdline = cmdline[:procCmdlineMax]
				}
				line += " " + strconv.Quote(cmdline)
			}
		}
		lines = append(lines, line)
	}
	if none && len(lines) > 0 {
		return "", fmt.Errorf("signal %q reported both no processes and processes", gpusignals.ProcessCmdline.ID())
	}
	return strings.Join(lines, "\n"), nil
}
