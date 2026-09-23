package cpu_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// numa_node_count is architecture-scoped, so its catalog must supply one.
func numaCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestNUMANodeCountDefinition(t *testing.T) {
	definition := cpu.NUMANodeCount()
	if definition.ID != "numa_node_count" || definition.Name != "NUMA Node Count" {
		t.Errorf("identity = %q/%q, want numa_node_count/NUMA Node Count", definition.ID, definition.Name)
	}
	if got, want := definition.Description, "Verifies a node brought online the expected number of NUMA nodes for its architecture."; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if definition.Category != health.CPU {
		t.Errorf("Category = %v, want CPU", definition.Category)
	}
	if got, want := definition.RequiredSignals, []string{cpu.SignalNUMANodeCount}; !slices.Equal(got, want) {
		t.Errorf("RequiredSignals = %v, want %v", got, want)
	}
	if definition.ExecutionClass != health.Passive {
		t.Errorf("ExecutionClass = %v, want Passive", definition.ExecutionClass)
	}
	wantContexts := []health.ExecutionContext{health.OperatorInfra, health.TenantGuest}
	if !slices.Equal(definition.ExecutionContexts, wantContexts) {
		t.Errorf("ExecutionContexts = %v, want %v", definition.ExecutionContexts, wantContexts)
	}
	if definition.SchedulingCadence != health.Continuous {
		t.Errorf("SchedulingCadence = %v, want Continuous", definition.SchedulingCadence)
	}
	if got, want := definition.Architectures, []string{"GB200", "GB300"}; !slices.Equal(got, want) {
		t.Errorf("Architectures = %v, want %v", got, want)
	}
	if definition.Provenance != health.NVIDIA || definition.MaturityLevel != health.Stable {
		t.Errorf("classification = %v/%q, want NVIDIA/stable", definition.Provenance, definition.MaturityLevel)
	}
	if !definition.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("RecommendedAction = %v, want generic reset/restart/escalate ladder", definition.RecommendedAction)
	}

	spec, ok := definition.Thresholds["expected_numa_node_count"]
	if !ok {
		t.Fatalf("Thresholds = %v, want expected_numa_node_count", definition.Thresholds)
	}
	if spec.Value != int64(34) {
		t.Errorf("Value = %v (%T), want int64(34)", spec.Value, spec.Value)
	}
	wantPerArchitecture := map[string]int64{
		"GB200": 34,
		"GB300": 34,
	}
	if len(spec.PerArchitecture) != len(wantPerArchitecture) {
		t.Errorf("PerArchitecture = %v, want exactly %v", spec.PerArchitecture, wantPerArchitecture)
	}
	for architecture, want := range wantPerArchitecture {
		if got := spec.PerArchitecture[architecture]; got != want {
			t.Errorf("PerArchitecture[%s] = %v (%T), want %v", architecture, got, got, want)
		}
	}
	if spec.Category != health.DefaultThreshold {
		t.Errorf("threshold Category = %v, want DefaultThreshold", spec.Category)
	}
	if spec.Unit != "count" {
		t.Errorf("Unit = %q, want count", spec.Unit)
	}

	if len(definition.Rules) != 1 {
		t.Fatalf("Rules = %v, want one topology rule", definition.Rules)
	}
	topologyRule := definition.Rules[0]
	if topologyRule.Expression != "numa_node_count != expected_numa_node_count" ||
		topologyRule.Severity != health.Critical {
		t.Errorf("topology rule = %q/%v, want NUMA-node inequality/Critical", topologyRule.Expression, topologyRule.Severity)
	}
	if topologyRule.Message != "Expected ${expected_numa_node_count} NUMA nodes online, found ${numa_node_count}." {
		t.Errorf("topology Message = %q, want canonical interpolation template", topologyRule.Message)
	}
	if topologyRule.HasRecommendedAction() {
		t.Errorf("topology RecommendedAction = %v, want inherited check action", topologyRule.RecommendedAction)
	}
}

func TestNUMANodeCount(t *testing.T) {
	tests := []struct {
		name         string
		nodeCount    int64
		wantSeverity health.Severity
		wantAction   health.RecommendedAction
		wantMessages []string
	}{
		{
			name:         "matches expected",
			nodeCount:    34,
			wantSeverity: health.Pass,
			wantAction:   health.NoAction,
		},
		{
			name:         "fewer than expected",
			nodeCount:    33,
			wantSeverity: health.Critical,
			wantAction:   health.GenericDrain(),
			wantMessages: []string{"Expected 34 NUMA nodes online, found 33."},
		},
		{
			// The rule is an equality, not a budget: an extra node fails
			// exactly like a missing one.
			name:         "more than expected",
			nodeCount:    35,
			wantSeverity: health.Critical,
			wantAction:   health.GenericDrain(),
			wantMessages: []string{"Expected 34 NUMA nodes online, found 35."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, err := numaCatalog().Evaluate("numa_node_count", cpu.CPUMeasurement{NUMANodeCount: tt.nodeCount})
			if err != nil {
				t.Fatal(err)
			}
			if verdict.Skipped || verdict.Severity != tt.wantSeverity {
				t.Fatalf("skipped=%v severity=%v, want evaluated %v", verdict.Skipped, verdict.Severity, tt.wantSeverity)
			}
			if !verdict.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", verdict.RecommendedAction, tt.wantAction)
			}
			if tt.wantMessages != nil {
				if got := verdict.Messages; !slices.Equal(got, tt.wantMessages) {
					t.Errorf("Messages = %q, want %q", got, tt.wantMessages)
				}
			}
		})
	}
}

// Every supported architecture has an explicit threshold, even when its value
// currently matches the base fallback.
func TestNUMANodeCount_PerArchitectureDefault(t *testing.T) {
	for _, architecture := range []string{"GB200", "GB300"} {
		t.Run(architecture, func(t *testing.T) {
			cat := catalog.New(
				catalog.WithArchitecture(architecture),
				catalog.WithExecutionContext(health.OperatorInfra),
			)

			verdict, err := cat.Evaluate("numa_node_count", cpu.CPUMeasurement{NUMANodeCount: 34})
			if err != nil {
				t.Fatal(err)
			}
			if verdict.Skipped || verdict.Severity != health.Pass {
				t.Fatalf("skipped=%v severity=%v, want evaluated PASS (34 == %s default)",
					verdict.Skipped, verdict.Severity, architecture)
			}
		})
	}
}

// expected_numa_node_count is a DefaultThreshold so operators can retune it
// per fleet without a release.
func TestNUMANodeCount_OverrideExpected(t *testing.T) {
	cat := numaCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "numa_node_count",
		ThresholdName: "expected_numa_node_count",
		Value:         int64(16),
	})))

	verdict, err := cat.Evaluate("numa_node_count", cpu.CPUMeasurement{NUMANodeCount: 16})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Skipped || verdict.Severity != health.Pass {
		t.Fatalf("skipped=%v severity=%v, want evaluated PASS (16 == overridden expected)", verdict.Skipped, verdict.Severity)
	}
	resolved := verdict.Thresholds["expected_numa_node_count"]
	if !resolved.Overridden || resolved.Value != int64(16) {
		t.Errorf("resolved = %+v, want overridden value 16", resolved)
	}
	if resolved.OriginalValue != int64(34) {
		t.Errorf("OriginalValue = %v, want the pre-override GB200 base 34", resolved.OriginalValue)
	}
}

// The expected counts are architecture-specific, so an architecture outside
// the list skips rather than being judged against another platform's value.
func TestNUMANodeCount_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("A100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	verdict, err := cat.Evaluate("numa_node_count", cpu.CPUMeasurement{NUMANodeCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Skipped || verdict.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with SIGNAL_UNAVAILABLE_ARCHITECTURE; got skipped=%v reason=%v",
			verdict.Skipped, verdict.SkipReason)
	}
}

// Evaluate requires WithArchitecture; a caller that supplies none gets an error
// rather than evaluating against a base Value with filtering off.
func TestNUMANodeCount_NoArchitectureErrors(t *testing.T) {
	cat := catalog.New(catalog.WithExecutionContext(health.OperatorInfra))

	_, err := cat.Evaluate("numa_node_count", cpu.CPUMeasurement{NUMANodeCount: 34})
	if !errors.Is(err, catalog.ErrArchitectureUnset) {
		t.Fatalf("Evaluate error = %v, want ErrArchitectureUnset", err)
	}
}

func TestNUMANodeCount_CollectionErrorsUseCollectionActions(t *testing.T) {
	tests := []struct {
		cause      error
		name       string
		wantAction health.RecommendedAction
	}{
		{
			cause:      errors.New("NUMA node online list unavailable"),
			name:       "collection failure",
			wantAction: health.Drain(health.RemediationActionCheckCollection),
		},
		{
			cause:      fmt.Errorf("cpu sensor: %w", health.ErrCollectionTimeout),
			name:       "collection timeout",
			wantAction: health.Drain(health.RemediationActionRetryCollection),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := cpu.CPUMeasurement{NUMANodeCount: 34}
			measurement.AddError(tt.cause, cpu.SignalNUMANodeCount)

			verdict, err := numaCatalog().Evaluate("numa_node_count", measurement)
			if err != nil {
				t.Fatal(err)
			}
			if verdict.Skipped || verdict.Severity != health.Critical {
				t.Fatalf("skipped=%v severity=%v, want evaluated CRITICAL", verdict.Skipped, verdict.Severity)
			}
			if !verdict.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", verdict.RecommendedAction, tt.wantAction)
			}
			if len(verdict.Messages) != 1 || !strings.Contains(verdict.Messages[0], tt.cause.Error()) {
				t.Errorf("Messages = %q, want surfaced cause %q", verdict.Messages, tt.cause)
			}
			if verdict.RecommendedAction.Equal(health.GenericDrain()) {
				t.Error("collection failure inherited the topology mismatch action")
			}
		})
	}
}

// A CPU-count collection failure must not fail numa_node_count: the two checks
// require disjoint signals from the one shared measurement.
func TestNUMANodeCount_UnaffectedByCPUCountCollectionFailure(t *testing.T) {
	measurement := cpu.CPUMeasurement{NUMANodeCount: 34}
	measurement.AddError(errors.New("CPU possible list unavailable"), cpu.SignalPossibleCPUCount)

	verdict, err := numaCatalog().Evaluate("numa_node_count", measurement)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Skipped || verdict.Severity != health.Pass {
		t.Fatalf("skipped=%v severity=%v, want evaluated PASS despite the sibling failure",
			verdict.Skipped, verdict.Severity)
	}
}
