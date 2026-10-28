// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// privateGPU is a private provider's GPU measurement. It carries the
// signals gpuPstateCheck reads, so it routes there alongside the built-in
// gpu.GPUMeasurement despite being a different Go type.
type privateGPU struct {
	UUID   string `json:"gpu_id"`
	Pstate int64  `json:"gpu_pstate"`
}

func (privateGPU) Category() health.Category           { return health.GPU }
func (privateGPU) Errors() []health.ErroredMeasurement { return nil }
func (m privateGPU) EntityID() string                  { return m.UUID }

// keyedPrivateGPU declares its key rather than relying on EntityID.
type keyedPrivateGPU struct {
	Label  string `json:"gpu_id"`
	key    health.EntityKey
	Pstate int64 `json:"gpu_pstate"`
}

func (keyedPrivateGPU) Category() health.Category           { return health.GPU }
func (keyedPrivateGPU) Errors() []health.ErroredMeasurement { return nil }
func (m keyedPrivateGPU) EntityID() string                  { return m.Label }
func (m keyedPrivateGPU) EntityKey() health.EntityKey       { return m.key }

// privateNode is a private host-wide measurement: it does not implement
// EntityIdentifier, so it is keyed as the node.
type privateNode struct {
	Count int64 `json:"gpu_count"`
}

func (privateNode) Category() health.Category           { return health.Node }
func (privateNode) Errors() []health.ErroredMeasurement { return nil }

func gpuCountProbe() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "gpu_count_probe",
		Category:          health.Node,
		RequiredSignals:   []string{node.SignalGPUCount},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules:             []health.Rule{{Expression: "gpu_count != 4", Severity: health.Critical, Message: "count"}},
	}
}

func probeCatalog(opts ...catalog.Option) *catalog.Catalog {
	cat := catalog.NewEmpty(append([]catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}, opts...)...)
	cat.MustRegister(gpuPstateCheck())
	cat.MustRegister(gpuCountProbe())
	return cat
}

func slotFailure(source, slot string) gpu.GPUMeasurement {
	m := gpu.ErroredGPU(source+"-index-"+slot, errors.New("handle unavailable"), gpu.MeasurementSignals()...)
	m.SetEntityKey(health.ProvisionalEntity(source, slot))
	return m
}

// wantAmbiguous asserts EvaluateAll refused to produce a verdict and that
// the error names the check and entity.
func wantAmbiguous(t *testing.T, cat *catalog.Catalog, checkID string, entity health.EntityKey, ms ...health.Measurement) *health.AmbiguousMeasurementError {
	t.Helper()
	nv, err := cat.EvaluateAll(ms...)
	if !errors.Is(err, health.ErrAmbiguousMeasurement) {
		t.Fatalf("EvaluateAll err = %v, want ErrAmbiguousMeasurement", err)
	}
	var ambiguous *health.AmbiguousMeasurementError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %T, want *health.AmbiguousMeasurementError", err)
	}
	if ambiguous.CheckID != checkID || ambiguous.Entity != entity {
		t.Errorf("ambiguity at check %q %v, want check %q %v", ambiguous.CheckID, ambiguous.Entity, checkID, entity)
	}
	if msg := err.Error(); !strings.Contains(msg, checkID) || !strings.Contains(msg, entity.String()) {
		t.Errorf("error %q does not name check %q and %v", msg, checkID, entity)
	}
	if len(nv.CheckResults) != 0 || len(nv.SkippedChecks) != 0 {
		t.Errorf("ambiguous run produced a partial verdict: %+v", nv)
	}
	return ambiguous
}

func TestEvaluateAll_RejectsDuplicateGPU(t *testing.T) {
	verified := health.VerifiedEntity("GPU-A")
	for _, tc := range []struct {
		name  string
		ms    []health.Measurement
		types []string
	}{
		{
			name:  "one provider emits it twice",
			ms:    []health.Measurement{gpu.GPUMeasurement{GPUID: "GPU-A"}, gpu.GPUMeasurement{GPUID: "GPU-A", GPUPstate: 8}},
			types: []string{"gpu.GPUMeasurement", "gpu.GPUMeasurement"},
		},
		{
			name:  "two providers with different types",
			ms:    []health.Measurement{gpu.GPUMeasurement{GPUID: "GPU-A"}, privateGPU{UUID: "GPU-A"}},
			types: []string{"gpu.GPUMeasurement", "catalog_test.privateGPU"},
		},
		{
			name: "a private provider declares the key",
			ms: []health.Measurement{
				gpu.GPUMeasurement{GPUID: "GPU-A"},
				keyedPrivateGPU{Label: "private-label", key: verified},
			},
			types: []string{"gpu.GPUMeasurement", "catalog_test.keyedPrivateGPU"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", verified, tc.ms...)
			if !slices.Equal(got.Types, tc.types) {
				t.Errorf("Types = %v, want %v", got.Types, tc.types)
			}
		})
	}
}

func TestEvaluateAll_RejectsDuplicateNodeMeasurement(t *testing.T) {
	t.Run("same type", func(t *testing.T) {
		wantAmbiguous(t, probeCatalog(), "gpu_count_probe", health.NodeEntity(),
			node.NodeMeasurement{GPUCount: 4}, node.NodeMeasurement{GPUCount: 4})
	})
	t.Run("different types", func(t *testing.T) {
		wantAmbiguous(t, probeCatalog(), "gpu_count_probe", health.NodeEntity(),
			node.NodeMeasurement{GPUCount: 4}, privateNode{Count: 4})
	})
}

// Distinct GPUs, whatever their providers, are distinct entities and each
// gets its own verdict.
func TestEvaluateAll_DistinctGPUsKeepDistinctVerdicts(t *testing.T) {
	nv, err := probeCatalog().EvaluateAll(
		gpu.GPUMeasurement{GPUID: "GPU-A"},
		privateGPU{UUID: "GPU-B", Pstate: 8},
		keyedPrivateGPU{Label: "label", key: health.VerifiedEntity("GPU-C")},
		node.NodeMeasurement{GPUCount: 4},
	)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	var n int
	for _, v := range nv.CheckResults {
		if v.CheckID == "gpu_pstate_probe" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("gpu_pstate_probe verdicts = %d, want 3", n)
	}
	if got := entitiesFor(t, nv, "gpu_pstate_probe"); !slices.Equal(got, []string{"GPU-B"}) {
		t.Errorf("AffectedEntities = %v, want [GPU-B]", got)
	}
}

// A slot number belongs to the source that enumerated it. NVML slot 0 and
// DCGM slot 0 are two unresolved GPUs, each failing on its own, while the
// same source reporting one slot twice is a duplicate.
func TestEvaluateAll_ProvisionalSlotsAreSourceScoped(t *testing.T) {
	t.Run("different sources stay distinct", func(t *testing.T) {
		nv, err := probeCatalog().EvaluateAll(slotFailure("nvml", "0"), slotFailure("dcgm", "0"))
		if err != nil {
			t.Fatalf("EvaluateAll: %v", err)
		}
		got := entitiesFor(t, nv, "gpu_pstate_probe")
		if want := []string{"nvml-index-0", "dcgm-index-0"}; !slices.Equal(got, want) {
			t.Errorf("AffectedEntities = %v, want %v", got, want)
		}
		for _, v := range nv.CheckResults {
			if v.CheckID == "gpu_pstate_probe" && v.Severity != health.Critical {
				t.Errorf("%v severity = %v, want CRITICAL", v.AffectedEntities, v.Severity)
			}
		}
	})
	t.Run("same source and slot conflict", func(t *testing.T) {
		wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", health.ProvisionalEntity("nvml", "0"),
			slotFailure("nvml", "0"), slotFailure("nvml", "0"))
	})
	t.Run("a slot does not join a UUID", func(t *testing.T) {
		// Index equality with a UUID-identified GPU's label proves nothing.
		if _, err := probeCatalog().EvaluateAll(gpu.GPUMeasurement{GPUID: "0"}, slotFailure("nvml", "0")); err != nil {
			t.Fatalf("EvaluateAll: %v", err)
		}
	})
}

// A provisional key is the identity of a failed lookup. Values under it would
// be a successful measurement of a GPU nothing ties to its other sources.
func TestEvaluateAll_ProvisionalIdentityCannotCarryValues(t *testing.T) {
	m := gpu.GPUMeasurement{GPUID: "nvml:0"}
	m.SetEntityKey(health.ProvisionalEntity("nvml", "0"))
	got := wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", health.ProvisionalEntity("nvml", "0"), m)
	if !strings.Contains(got.Reason, "verified ID") {
		t.Errorf("Reason = %q, want it to require a verified ID", got.Reason)
	}

	// Source-level identities follow the same rule.
	m.SetEntityKey(health.SourceEntity("nvml"))
	wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", health.SourceEntity("nvml"), m)
}

// A check with no required signals reads every signal, so a provisional
// measurement feeding it must have lost all of them. One failed field would
// leave values under an identity nothing verifies.
func TestEvaluateAll_ProvisionalIdentityFeedingAnySignalCheck(t *testing.T) {
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	cat.MustRegister(health.CheckDefinition{
		ID:                "reads_any_gpu_signal",
		Category:          health.GPU,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules:             []health.Rule{{Expression: "gpu_pstate != 0", Severity: health.Critical, Message: "pstate"}},
	})

	if _, err := cat.EvaluateAll(slotFailure("nvml", "0")); err != nil {
		t.Fatalf("fully failed slot: EvaluateAll: %v", err)
	}

	partial := gpu.GPUMeasurement{GPUID: "nvml-index-0", GPUPstate: 8}
	partial.AddError(errors.New("power read failed"), gpu.SignalAveragePowerDrawW)
	partial.SetEntityKey(health.ProvisionalEntity("nvml", "0"))
	wantAmbiguous(t, cat, "reads_any_gpu_signal", health.ProvisionalEntity("nvml", "0"), partial)
}

func TestEvaluateAll_MissingIdentityFailsClosed(t *testing.T) {
	for _, key := range []health.EntityKey{
		health.ProvisionalEntity("", "0"),
		health.ProvisionalEntity("nvml", ""),
		health.SourceEntity(" "),
		health.VerifiedEntity(""),
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := gpu.ErroredGPU("gpu", errors.New("lost"), gpu.MeasurementSignals()...)
			m.SetEntityKey(key)
			wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", key, m)
		})
	}
}

// A GPU whose producer could not name it may report the collection failure
// that left it unnamed, which fails the check. Two such failures cannot be
// told apart.
func TestEvaluateAll_UnidentifiedCollectionFailures(t *testing.T) {
	lost := gpu.ErroredGPU("", errors.New("lost"), gpu.MeasurementSignals()...)
	nv, err := probeCatalog().EvaluateAll(lost, gpu.GPUMeasurement{GPUID: "GPU-A"})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	// GPU-A passes; the unidentified failure is the one CRITICAL.
	var severities []health.Severity
	for _, v := range nv.CheckResults {
		if v.CheckID == "gpu_pstate_probe" {
			severities = append(severities, v.Severity)
		}
	}
	slices.Sort(severities)
	if want := []health.Severity{health.Pass, health.Critical}; !slices.Equal(severities, want) {
		t.Errorf("gpu_pstate_probe severities = %v, want %v", severities, want)
	}
	wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", health.EntityKey{}, lost, lost)
}

// Collected values without an identity may describe a GPU another provider
// also reports, as when an ID-less nvidia-smi path is left running beside
// NVML. Evaluation cannot tell, so it rejects them even from a sole provider.
func TestEvaluateAll_UnidentifiedValuesAreRejected(t *testing.T) {
	// The power read failed, but the probe reads gpu_pstate, which was
	// collected, so the check would still evaluate a value.
	partial := gpu.GPUMeasurement{GPUPstate: 8}
	partial.AddError(errors.New("power read failed"), gpu.SignalAveragePowerDrawW)

	for _, tc := range []struct {
		name string
		ms   []health.Measurement
	}{
		{name: "sole provider", ms: []health.Measurement{gpu.GPUMeasurement{GPUPstate: 0}}},
		{name: "beside an identified GPU", ms: []health.Measurement{gpu.GPUMeasurement{GPUID: "GPU-A"}, gpu.GPUMeasurement{GPUPstate: 8}}},
		{name: "beside an unidentified failure", ms: []health.Measurement{gpu.ErroredGPU("", errors.New("lost")), gpu.GPUMeasurement{}}},
		{name: "failure on a signal the check does not read", ms: []health.Measurement{partial}},
		{name: "private type with an empty EntityID", ms: []health.Measurement{privateGPU{Pstate: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wantAmbiguous(t, probeCatalog(), "gpu_pstate_probe", health.EntityKey{}, tc.ms...)
			if !strings.Contains(got.Reason, "name no entity") {
				t.Errorf("Reason = %q, want it to say the values name no entity", got.Reason)
			}
		})
	}
}

// The guard is per check. Duplicates reaching only a check that will not run
// produce no competing verdicts, so they do not stop the run.
func TestEvaluateAll_DuplicatesFeedingOnlySkippedChecks(t *testing.T) {
	cat := probeCatalog(catalog.WithDisabledChecks("gpu_pstate_probe"))
	if _, err := cat.EvaluateAll(gpu.GPUMeasurement{GPUID: "GPU-A"}, gpu.GPUMeasurement{GPUID: "GPU-A"}); err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
}

// The single-check path applies the identity rules a lone measurement can
// break.
func TestEvaluate_RejectsInvalidIdentity(t *testing.T) {
	m := gpu.GPUMeasurement{GPUID: "nvml:0"}
	m.SetEntityKey(health.ProvisionalEntity("nvml", "0"))
	if _, err := probeCatalog().Evaluate("gpu_pstate_probe", m); !errors.Is(err, health.ErrAmbiguousMeasurement) {
		t.Fatalf("Evaluate err = %v, want ErrAmbiguousMeasurement", err)
	}
}
