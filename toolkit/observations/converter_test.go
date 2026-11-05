// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// eccMeasurement is a caller's per-GPU type, as a consumer would write it.
type eccMeasurement struct {
	GPUID string `json:"gpu_id"`
	Entity
	DoubleBit uint64 `json:"ecc_double_bit"`
	Pending   bool   `json:"row_remap_pending"`
}

func (eccMeasurement) Category() health.Category { return health.GPU }
func (m eccMeasurement) EntityID() string        { return m.GPUID }

var eccFields = map[string]string{
	gpusignals.ECCDoubleBitVolatileTotal.ID(): "ecc_double_bit",
	gpusignals.RowRemapPending.ID():           "row_remap_pending",
}

func eccConverter(t *testing.T) *GPUConverter {
	t.Helper()
	c, err := NewGPUConverter[eccMeasurement](eccFields)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func onGPU(o *observation.Observation, uuid string) *observation.Observation {
	o.Target = observation.GPUTarget(uuid)
	return o
}

func failedFieldsOf(m health.Measurement) []string {
	var fields []string
	for _, e := range m.Errors() {
		fields = append(fields, e.FailedSignals()...)
	}
	slices.Sort(fields)
	return slices.Compact(fields)
}

func TestGPUConverterBuildsOnePerGPU(t *testing.T) {
	ms := eccConverter(t).ToMeasurements([]*observation.Observation{
		onGPU(valueObservation(gpusignals.ECCDoubleBitVolatileTotal.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: 3}}), "GPU-0"),
		onGPU(valueObservation(gpusignals.RowRemapPending.ID(), boolValue(true)), "GPU-0"),
		onGPU(failedObservation(gpusignals.ECCDoubleBitVolatileTotal.ID(), &observation.CollectionError{Source: "nvml", Operation: "GetTotalEccErrors", Category: observation.CollectionErrorCategorySourceError}), "GPU-1"),
		onGPU(valueObservation(gpusignals.PowerDraw.ID(), doubleValue(75)), "GPU-0"), // not mapped: ignored
	})
	if got, want := keysOf(ms), []health.EntityKey{health.VerifiedEntity("GPU-0"), health.VerifiedEntity("GPU-1")}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	gpu0, gpu1 := ms[0].(eccMeasurement), ms[1].(eccMeasurement)
	if gpu0.GPUID != "GPU-0" || gpu0.DoubleBit != 3 || !gpu0.Pending || len(gpu0.Errors()) != 0 {
		t.Errorf("GPU-0 = %+v", gpu0)
	}
	// The failed read and the never-observed field both fail, so neither
	// check can pass on a zero value.
	if got := failedFieldsOf(gpu1); !slices.Equal(got, []string{"ecc_double_bit", "row_remap_pending"}) {
		t.Errorf("GPU-1 failed fields = %v", got)
	}
}

func TestGPUConverterFailsUnidentifiedAndUnsupported(t *testing.T) {
	slot := failedObservation(gpusignals.ECCDoubleBitVolatileTotal.ID(), &observation.CollectionError{Source: "nvml", Operation: "GetDevices", Category: observation.CollectionErrorCategoryUnavailable})
	slot.Source, slot.Target = "nvml", observation.GPUSourceLocalTarget("1")
	unsupported := onGPU(noValueObservation(gpusignals.RowRemapPending.ID(), observation.NoValueReasonNotSupported), "GPU-0")
	ms := eccConverter(t).ToMeasurements([]*observation.Observation{
		onGPU(valueObservation(gpusignals.ECCDoubleBitVolatileTotal.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: 0}}), "GPU-0"),
		unsupported, slot,
	})
	if got, want := keysOf(ms), []health.EntityKey{health.VerifiedEntity("GPU-0"), health.ProvisionalEntity("nvml", "1")}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if got := failedFieldsOf(ms[0]); !slices.Equal(got, []string{"row_remap_pending"}) {
		t.Errorf("GPU-0 failed fields = %v, want the unsupported one", got)
	}
	if got := failedFieldsOf(ms[1]); !slices.Contains(got, gpuIDField) {
		t.Errorf("slot failed fields = %v, want gpu_id", got)
	}
}

func TestGPUConverterErrored(t *testing.T) {
	m := eccConverter(t).Errored("collect.ecc", errors.New("registration failed"))
	if health.KeyOf(m) != health.SourceEntity("collect.ecc") || !slices.Equal(failedFieldsOf(m), []string{"ecc_double_bit", "row_remap_pending"}) {
		t.Fatalf("errored = %+v keyed %v", m, health.KeyOf(m))
	}
}

type noEntityMeasurement struct {
	GPUID string `json:"gpu_id"`
	Value int64  `json:"value"`
}

func (noEntityMeasurement) Category() health.Category           { return health.GPU }
func (noEntityMeasurement) Errors() []health.ErroredMeasurement { return nil }

type noGPUIDMeasurement struct {
	Entity
	Value int64 `json:"value"`
}

func (noGPUIDMeasurement) Category() health.Category { return health.GPU }

func TestNewGPUConverterRejectsUnusableTypes(t *testing.T) {
	for _, test := range []struct {
		build func() error
		want  string
	}{
		{want: "embed observations.Entity", build: func() error {
			_, err := NewGPUConverter[noEntityMeasurement](map[string]string{"s": "value"})
			return err
		}},
		{want: `no string field tagged json:"gpu_id"`, build: func() error {
			_, err := NewGPUConverter[noGPUIDMeasurement](map[string]string{"s": "value"})
			return err
		}},
		{want: "not a catalog signal", build: func() error {
			_, err := NewGPUConverter[eccMeasurement](map[string]string{"s": "typo"})
			return err
		}},
		{want: "not a catalog signal", build: func() error {
			_, err := NewGPUConverter[eccMeasurement](map[string]string{"s": gpuIDField})
			return err
		}},
		{want: "no signals map", build: func() error {
			_, err := NewGPUConverter[eccMeasurement](nil)
			return err
		}},
	} {
		if err := test.build(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("err = %v, want %q", err, test.want)
		}
	}
}
