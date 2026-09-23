package toolkit_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

type cpuBackendFunc func(context.Context) (cpu.CPUMeasurement, error)

func (f cpuBackendFunc) MeasureCPU(ctx context.Context) (cpu.CPUMeasurement, error) {
	return f(ctx)
}

func cpuAvailabilityCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "cpu_availability_test",
		Name:        "CPU Availability (test)",
		Description: "CPU availability must be observable",
		Category:    health.CPU,
		RequiredSignals: []string{
			cpu.SignalPossibleCPUCount,
			cpu.SignalOnlineCPUCount,
		},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Rules: []health.Rule{
			{
				Expression: "false",
				Severity:   health.Critical,
				Message:    "unreachable test rule",
			},
		},
	}
}

func cpuCatalog() *catalog.Catalog {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture(catalogtest.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(cpuAvailabilityCheck())
	return cat
}

func TestCPUSensor_MockEmits(t *testing.T) {
	sensor := toolkit.NewCPUSensor(toolkit.MockCPU(
		cpu.CPUMeasurement{
			PossibleCPUCount: 144,
			OnlineCPUCount:   143,
		},
	))

	if got := sensor.Name(); got != "cpu" {
		t.Errorf("Name() = %q, want cpu", got)
	}

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	measurement, ok := measurements[0].(cpu.CPUMeasurement)
	if !ok {
		t.Fatalf("measurement is %T, want cpu.CPUMeasurement", measurements[0])
	}
	if measurement.PossibleCPUCount != 144 {
		t.Errorf("PossibleCPUCount = %d, want 144", measurement.PossibleCPUCount)
	}
	if measurement.OnlineCPUCount != 143 {
		t.Errorf("OnlineCPUCount = %d, want 143", measurement.OnlineCPUCount)
	}
}

func TestCPUSensor_MeasureForwardsContextAndError(t *testing.T) {
	type contextKey struct{}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "sentinel"))
	cancel()

	cause := errors.New("CPU backend unavailable")
	var gotContext context.Context
	sensor := toolkit.NewCPUSensor(cpuBackendFunc(func(ctx context.Context) (cpu.CPUMeasurement, error) {
		gotContext = ctx
		return cpu.CPUMeasurement{}, cause
	}))

	measurements, err := sensor.Measure(ctx)
	if measurements != nil {
		t.Errorf("measurements = %v, want nil", measurements)
	}
	if err != cause {
		t.Errorf("error = %v, want unchanged cause %v", err, cause)
	}
	if gotContext != ctx {
		t.Fatal("Measure did not forward the exact incoming context")
	}
	if got := gotContext.Value(contextKey{}); got != "sentinel" {
		t.Errorf("context value = %v, want sentinel", got)
	}
	if !errors.Is(gotContext.Err(), context.Canceled) {
		t.Errorf("context error = %v, want context.Canceled", gotContext.Err())
	}
}

func TestCPUSensor_ErroredAttributesAllSignals(t *testing.T) {
	sensor := toolkit.NewCPUSensor(toolkit.MockCPU(cpu.CPUMeasurement{}))
	cause := errors.New("CPU availability unavailable")

	measurements := sensor.Errored(cause)
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	measurement, ok := measurements[0].(cpu.CPUMeasurement)
	if !ok {
		t.Fatalf("measurement is %T, want cpu.CPUMeasurement", measurements[0])
	}
	errs := measurement.Errors()
	if len(errs) != 1 {
		t.Fatalf("got %d measurement errors, want 1", len(errs))
	}
	if !errors.Is(errs[0].Err(), cause) {
		t.Errorf("error = %v, want cause %v", errs[0].Err(), cause)
	}
	if got, want := errs[0].FailedSignals(), cpu.MeasurementSignals; !slices.Equal(got, want) {
		t.Errorf("FailedSignals = %v, want %v", got, want)
	}
}

func TestCollectWithOptions_CPUTimeoutDegradesToCritical(t *testing.T) {
	sensor := toolkit.NewCPUSensor(cpuBackendFunc(func(ctx context.Context) (cpu.CPUMeasurement, error) {
		<-ctx.Done()
		return cpu.CPUMeasurement{}, ctx.Err()
	}))

	verdict, err := toolkit.CollectWithOptions(cpuCatalog(), toolkit.CollectionOptions{Timeout: 20 * time.Millisecond}, sensor)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error: %v", err)
	}
	if verdict.OverallSeverity != health.Critical {
		t.Fatalf("overall = %v, want CRITICAL", verdict.OverallSeverity)
	}
	if len(verdict.CheckResults) != 1 {
		t.Fatalf("got %d check results, want 1", len(verdict.CheckResults))
	}
	result := verdict.CheckResults[0]
	if result.CheckID != "cpu_availability_test" {
		t.Errorf("CheckID = %q, want cpu_availability_test", result.CheckID)
	}
	if result.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL", result.Severity)
	}
	if len(result.Messages) != 1 || !strings.Contains(result.Messages[0], health.ErrCollectionTimeout.Error()) {
		t.Errorf("messages = %v, want collection timeout", result.Messages)
	}
	if got, want := result.RecommendedAction, health.Drain(health.RemediationActionRetryCollection); !got.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", got, want)
	}
}

func TestCollectWithOptions_CPUBackendFailureProducesResult(t *testing.T) {
	cause := errors.New("CPU backend unavailable")
	sensor := toolkit.NewCPUSensor(cpuBackendFunc(func(context.Context) (cpu.CPUMeasurement, error) {
		return cpu.CPUMeasurement{}, cause
	}))

	verdict, err := toolkit.CollectWithOptions(cpuCatalog(), toolkit.CollectionOptions{Timeout: time.Second}, sensor)
	if err != nil {
		t.Fatalf("CollectWithOptions returned error: %v", err)
	}
	if verdict.OverallSeverity != health.Critical {
		t.Fatalf("overall = %v, want CRITICAL", verdict.OverallSeverity)
	}
	if len(verdict.SkippedChecks) != 0 {
		t.Errorf("skipped checks = %+v, want none", verdict.SkippedChecks)
	}
	if len(verdict.CheckResults) != 1 {
		t.Fatalf("got %d check results, want 1", len(verdict.CheckResults))
	}
	result := verdict.CheckResults[0]
	if result.CheckID != "cpu_availability_test" {
		t.Errorf("CheckID = %q, want cpu_availability_test", result.CheckID)
	}
	if len(result.Messages) != 1 || !strings.Contains(result.Messages[0], cause.Error()) {
		t.Errorf("messages = %v, want backend failure", result.Messages)
	}
	if got, want := result.RecommendedAction, health.Drain(health.RemediationActionCheckCollection); !got.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", got, want)
	}
}
