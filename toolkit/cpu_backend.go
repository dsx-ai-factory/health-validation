// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
)

// CPUBackend supplies exactly one host-wide CPU measurement.
type CPUBackend interface {
	MeasureCPU(context.Context) (cpu.CPUMeasurement, error)
}

// MockCPU returns a [CPUBackend] that emits the provided measurement.
func MockCPU(measurement cpu.CPUMeasurement) CPUBackend {
	return mockCPUBackend{measurement: measurement}
}

type mockCPUBackend struct {
	measurement cpu.CPUMeasurement
}

func (m mockCPUBackend) MeasureCPU(context.Context) (cpu.CPUMeasurement, error) {
	return m.measurement, nil
}
