// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"context"
	"errors"
	"fmt"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

// cpuCollectSensor adapts the CPU collect sensor to toolkit.Sensor.
type cpuCollectSensor struct{ sensor collect.Sensor }

func (cpuCollectSensor) Name() string { return "cpu" }

func (s cpuCollectSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	// Skip the activity signals the sensor also offers; no check reads them.
	batch, err := s.sensor.Measure(ctx, collect.WithSignalID(observations.MappedSignalsFor(health.CPU)...))
	if err != nil {
		return nil, err
	}
	ms := observations.ToMeasurements(batch.GetObservations())
	if len(ms) == 0 {
		// An empty conversion would skip the CPU checks; fail them instead.
		return nil, errors.New("no CPU observations were collected")
	}
	return ms, nil
}

func (cpuCollectSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{cpu.ErroredCPU(err)}
}

// Example_collectSensor runs a collect sensor through toolkit.Collect, the
// replacement for the removed toolkit.NewCPUSensor and SysfsCPU. Pass nil
// instead of the in-memory sysfs to read the host.
func Example_collectSensor() {
	sysfs := fstest.MapFS{
		"sys/devices/system/cpu/possible": {Data: []byte("0-3\n")},
		"sys/devices/system/cpu/online":   {Data: []byte("0-2\n")},
		"sys/devices/system/node/online":  {Data: []byte("0\n")},
	}
	cat := catalog.New(catalog.WithArchitecture("GB200"))

	verdict, err := toolkit.Collect(cat, cpuCollectSensor{sensor: cpusensor.New(sysfs)})
	if err != nil {
		panic(err)
	}
	for _, r := range verdict.CheckResults {
		if r.CheckID == "cpu_count" {
			fmt.Printf("%s %s\n", r.CheckID, r.Severity)
		}
	}

	// Output:
	// cpu_count CRITICAL
}
