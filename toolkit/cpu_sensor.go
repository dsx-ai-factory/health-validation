// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// CPUSensor collects host-wide CPU measurements through a pluggable backend.
type CPUSensor struct {
	backend CPUBackend
}

// NewCPUSensor creates a CPU sensor backed by the given backend.
func NewCPUSensor(backend CPUBackend) *CPUSensor {
	return &CPUSensor{backend: backend}
}

func (s *CPUSensor) Name() string { return "cpu" }

func (s *CPUSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	measurement, err := s.backend.MeasureCPU(ctx)
	if err != nil {
		return nil, err
	}
	return []health.Measurement{measurement}, nil
}

func (s *CPUSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{cpu.ErroredCPU(err)}
}
