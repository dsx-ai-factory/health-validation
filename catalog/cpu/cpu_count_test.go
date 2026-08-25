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

func cpuCatalog() *catalog.Catalog {
	return catalog.New(
		catalog.WithArchitecture(catalog.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
}

func TestCPUCountDefinition(t *testing.T) {
	definition := cpu.CPUCount()
	if definition.ID != "cpu_count" || definition.Name != "System CPU Count" {
		t.Errorf("identity = %q/%q, want cpu_count/System CPU Count", definition.ID, definition.Name)
	}
	if got, want := definition.Description, "Verifies every logical CPU in the Linux kernel's possible mask is online and available for scheduling."; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if definition.Category != health.CPU {
		t.Errorf("Category = %v, want CPU", definition.Category)
	}
	if got, want := definition.RequiredSignals, []string{
		cpu.SignalPossibleCPUCount,
		cpu.SignalOnlineCPUCount,
	}; !slices.Equal(got, want) {
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
	if len(definition.Architectures) != 0 {
		t.Errorf("Architectures = %v, want unrestricted", definition.Architectures)
	}
	if definition.Provenance != health.NVIDIA || definition.MaturityLevel != health.Stable {
		t.Errorf("classification = %v/%v, want NVIDIA/stable", definition.Provenance, definition.MaturityLevel)
	}
	if definition.RecommendedAction != health.Drain(health.DiagnosticActionCheckMechanicals) {
		t.Errorf("RecommendedAction = %v, want check_mechanicals", definition.RecommendedAction)
	}
	if len(definition.Thresholds) != 0 {
		t.Errorf("Thresholds = %v, want none", definition.Thresholds)
	}
	if len(definition.Rules) != 1 {
		t.Fatalf("Rules = %v, want one mechanical rule", definition.Rules)
	}

	mechanicalRule := definition.Rules[0]
	if mechanicalRule.Expression != "online_cpu_count != possible_cpu_count" ||
		mechanicalRule.Severity != health.Critical {
		t.Errorf("mechanical rule = %q/%v, want CPU-count inequality/Critical", mechanicalRule.Expression, mechanicalRule.Severity)
	}
	if mechanicalRule.Message != "Expected all ${possible_cpu_count} possible logical CPUs to be online; found ${online_cpu_count} online." {
		t.Errorf("mechanical Message = %q, want canonical interpolation template", mechanicalRule.Message)
	}
	if mechanicalRule.HasRecommendedAction() {
		t.Errorf("mechanical RecommendedAction = %v, want inherited check action", mechanicalRule.RecommendedAction)
	}
}

func TestCPUCount_AllPossibleCPUsOnline(t *testing.T) {
	verdict, err := cpuCatalog().Evaluate("cpu_count", cpu.CPUMeasurement{
		PossibleCPUCount: 144,
		OnlineCPUCount:   144,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Skipped || verdict.Severity != health.Pass {
		t.Fatalf("skipped=%v severity=%v, want evaluated PASS", verdict.Skipped, verdict.Severity)
	}
	if verdict.RecommendedAction != health.NoAction {
		t.Errorf("RecommendedAction = %v, want NoAction", verdict.RecommendedAction)
	}
}

func TestCPUCount_OfflineCPU(t *testing.T) {
	verdict, err := cpuCatalog().Evaluate("cpu_count", cpu.CPUMeasurement{
		PossibleCPUCount: 144,
		OnlineCPUCount:   143,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Skipped || verdict.Severity != health.Critical {
		t.Fatalf("skipped=%v severity=%v, want evaluated CRITICAL", verdict.Skipped, verdict.Severity)
	}
	if verdict.RecommendedAction != health.Drain(health.DiagnosticActionCheckMechanicals) {
		t.Errorf("RecommendedAction = %v, want check_mechanicals", verdict.RecommendedAction)
	}
	if got, want := verdict.Messages, []string{"Expected all 144 possible logical CPUs to be online; found 143 online."}; !slices.Equal(got, want) {
		t.Errorf("Messages = %q, want %q", got, want)
	}
}

func TestCPUCount_CollectionErrorsUseCollectionActions(t *testing.T) {
	tests := []struct {
		cause      error
		name       string
		signal     string
		wantAction health.RecommendedAction
	}{
		{
			cause:      errors.New("CPU possible list unavailable"),
			name:       "possible collection failure",
			signal:     cpu.SignalPossibleCPUCount,
			wantAction: health.Drain(health.DiagnosticActionCheckCollection),
		},
		{
			cause:      errors.New("CPU online list unavailable"),
			name:       "online collection failure",
			signal:     cpu.SignalOnlineCPUCount,
			wantAction: health.Drain(health.DiagnosticActionCheckCollection),
		},
		{
			cause:      fmt.Errorf("cpu sensor: %w", health.ErrCollectionTimeout),
			name:       "collection timeout",
			signal:     cpu.SignalOnlineCPUCount,
			wantAction: health.Drain(health.DiagnosticActionRetryCollection),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := cpu.CPUMeasurement{
				PossibleCPUCount: 144,
				OnlineCPUCount:   144,
			}
			measurement.AddError(tt.cause, tt.signal)

			verdict, err := cpuCatalog().Evaluate("cpu_count", measurement)
			if err != nil {
				t.Fatal(err)
			}
			if verdict.Skipped || verdict.Severity != health.Critical {
				t.Fatalf("skipped=%v severity=%v, want evaluated CRITICAL", verdict.Skipped, verdict.Severity)
			}
			if verdict.RecommendedAction != tt.wantAction {
				t.Errorf("RecommendedAction = %v, want %v", verdict.RecommendedAction, tt.wantAction)
			}
			if len(verdict.Messages) != 1 || !strings.Contains(verdict.Messages[0], tt.cause.Error()) {
				t.Errorf("Messages = %q, want surfaced cause %q", verdict.Messages, tt.cause)
			}
			if verdict.RecommendedAction == health.Drain(health.DiagnosticActionCheckMechanicals) {
				t.Error("collection failure inherited the hardware mismatch action")
			}
		})
	}
}
