// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"fmt"
	"os"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
)

const (
	cpuPossiblePath = "/sys/devices/system/cpu/possible"
	cpuOnlinePath   = "/sys/devices/system/cpu/online"
	// numaNodeOnlinePath is the sysfs list of online NUMA nodes (e.g. "0-33").
	// The kernel creates a node directory only for an online node, so parsing
	// this list gives the same total a topology tool reports.
	numaNodeOnlinePath = "/sys/devices/system/node/online"
)

type sysfsCPUFileReader func(string) ([]byte, error)

type sysfsCPUBackend struct {
	readFile sysfsCPUFileReader
}

// SysfsCPU returns a backend that collects CPU availability and NUMA topology
// from sysfs.
//
// Deprecated: toolkit/app collects CPU topology through the collect sensor in
// collect/sensors/cpu. SysfsCPU is removed with the other legacy backends.
func SysfsCPU() CPUBackend {
	return &sysfsCPUBackend{readFile: os.ReadFile}
}

func (b *sysfsCPUBackend) MeasureCPU(ctx context.Context) (cpu.CPUMeasurement, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}

	var measurement cpu.CPUMeasurement
	data, err := b.readFile(cpuPossiblePath)
	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	if err != nil {
		measurement.AddError(
			fmt.Errorf("read CPU possible list %s: %w", cpuPossiblePath, err),
			cpu.SignalPossibleCPUCount,
		)
	} else if count, parseErr := parseSysfsListCount(data); parseErr != nil {
		measurement.AddError(
			fmt.Errorf("parse CPU possible list %s: %w", cpuPossiblePath, parseErr),
			cpu.SignalPossibleCPUCount,
		)
	} else {
		measurement.PossibleCPUCount = count
	}

	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	data, err = b.readFile(cpuOnlinePath)
	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	if err != nil {
		measurement.AddError(
			fmt.Errorf("read CPU online list %s: %w", cpuOnlinePath, err),
			cpu.SignalOnlineCPUCount,
		)
	} else if count, parseErr := parseSysfsListCount(data); parseErr != nil {
		measurement.AddError(
			fmt.Errorf("parse CPU online list %s: %w", cpuOnlinePath, parseErr),
			cpu.SignalOnlineCPUCount,
		)
	} else {
		measurement.OnlineCPUCount = count
	}

	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	data, err = b.readFile(numaNodeOnlinePath)
	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	if err != nil {
		measurement.AddError(
			fmt.Errorf("read NUMA node online list %s: %w", numaNodeOnlinePath, err),
			cpu.SignalNUMANodeCount,
		)
	} else if count, parseErr := parseSysfsListCount(data); parseErr != nil {
		measurement.AddError(
			fmt.Errorf("parse NUMA node online list %s: %w", numaNodeOnlinePath, parseErr),
			cpu.SignalNUMANodeCount,
		)
	} else {
		measurement.NUMANodeCount = count
	}

	if contextErr := ctx.Err(); contextErr != nil {
		return cpu.CPUMeasurement{}, fmt.Errorf("collect CPU availability: %w", contextErr)
	}
	return measurement, nil
}
