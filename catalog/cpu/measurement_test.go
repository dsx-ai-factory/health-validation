// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestCPUMeasurementContract(t *testing.T) {
	if got, want := cpu.SignalPossibleCPUCount, "possible_cpu_count"; got != want {
		t.Errorf("SignalPossibleCPUCount = %q, want %q", got, want)
	}
	if got, want := cpu.SignalOnlineCPUCount, "online_cpu_count"; got != want {
		t.Errorf("SignalOnlineCPUCount = %q, want %q", got, want)
	}
	if got, want := cpu.SignalNUMANodeCount, "numa_node_count"; got != want {
		t.Errorf("SignalNUMANodeCount = %q, want %q", got, want)
	}
	if got, want := cpu.MeasurementSignals(), []string{"possible_cpu_count", "online_cpu_count", "numa_node_count"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MeasurementSignals = %v, want %v", got, want)
	}

	var measurement health.Measurement = cpu.CPUMeasurement{
		PossibleCPUCount: 144,
		OnlineCPUCount:   143,
		NUMANodeCount:    34,
	}

	if got := measurement.Category(); got != health.CPU {
		t.Errorf("Category = %v, want cpu", got)
	}
	if got := measurement.Errors(); len(got) != 0 {
		t.Errorf("Errors = %v, want none", got)
	}

	data, err := json.Marshal(measurement)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(data), `{"possible_cpu_count":144,"online_cpu_count":143,"numa_node_count":34}`; got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

func TestErroredCPUDefaultsToAllSignals(t *testing.T) {
	cause := errors.New("CPU availability unavailable")
	m := cpu.ErroredCPU(cause)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors length = %d, want 1", len(errs))
	}
	if !errors.Is(errs[0].Err(), cause) {
		t.Errorf("Err = %v, want %v", errs[0].Err(), cause)
	}
	if got, want := errs[0].FailedSignals(), cpu.MeasurementSignals(); !reflect.DeepEqual(got, want) {
		t.Errorf("FailedSignals = %v, want %v", got, want)
	}
}

func TestCPUMeasurementAddErrorAccumulates(t *testing.T) {
	first := errors.New("possible CPU collection failed")
	second := errors.New("online CPU collection failed")
	m := cpu.CPUMeasurement{
		PossibleCPUCount: 144,
		OnlineCPUCount:   143,
	}
	m.AddError(first, cpu.SignalPossibleCPUCount)
	m.AddError(second, cpu.SignalOnlineCPUCount)

	errs := m.Errors()
	if len(errs) != 2 {
		t.Fatalf("Errors length = %d, want 2", len(errs))
	}
	if !errors.Is(errs[0].Err(), first) || !errors.Is(errs[1].Err(), second) {
		t.Errorf("Errors = [%v, %v], want [%v, %v]", errs[0].Err(), errs[1].Err(), first, second)
	}
	if got, want := errs[0].FailedSignals(), []string{cpu.SignalPossibleCPUCount}; !reflect.DeepEqual(got, want) {
		t.Errorf("first FailedSignals = %v, want %v", got, want)
	}
	if got, want := errs[1].FailedSignals(), []string{cpu.SignalOnlineCPUCount}; !reflect.DeepEqual(got, want) {
		t.Errorf("second FailedSignals = %v, want %v", got, want)
	}
}
