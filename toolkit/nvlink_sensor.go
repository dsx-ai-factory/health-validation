// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// NVLinkSensor collects NVLink domain measurements via a pluggable
// [NVLinkBackend]. It implements [Sensor] and delegates to the backend's
// [NVLinkBackend.MeasureNVLink] method. A full multi-domain source (e.g.
// NVMLBackend) satisfies NVLinkBackend too, so one source can feed this and
// the GPU/Node sensors from a single enumeration.
type NVLinkSensor struct {
	backend NVLinkBackend
}

// NewNVLinkSensor creates an NVLink sensor backed by the given backend.
//
// For testing:
//
//	sensor := toolkit.NewNVLinkSensor(toolkit.MockNVLink(
//	    nvlink.NVLinkMeasurement{},
//	))
func NewNVLinkSensor(backend NVLinkBackend) *NVLinkSensor {
	return &NVLinkSensor{backend: backend}
}

func (s *NVLinkSensor) Name() string { return "nvlink" }

// Measure does not use the context for the same reason as
// [GPUSensor.Measure]: the NVLink signals come from the cgo NVML backend,
// bounded by the caller's watchdog.
func (s *NVLinkSensor) Measure(_ context.Context) ([]health.Measurement, error) {
	return s.backend.MeasureNVLink()
}

// Errored reports the whole NVLink domain as unobservable so NVLink
// checks fail Critical when collection could not run.
func (s *NVLinkSensor) Errored(err error) []health.Measurement {
	m := nvlink.ErroredNVLink(s.Name(), err, nvlink.MeasurementSignals()...)
	m.SetEntityKey(health.SourceEntity(sensorSource(s.Name())))
	return []health.Measurement{m}
}

// ResourceKey groups this sensor with the others sharing its backend (see
// [GPUSensor.ResourceKey]).
func (s *NVLinkSensor) ResourceKey() string { return backendResourceKey(s.backend) }
