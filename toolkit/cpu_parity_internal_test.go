package toolkit

import (
	"context"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	cpusensor "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
)

// deniedFS fails reads of one path with a permission error.
type deniedFS struct {
	fs.FS
	path string
}

func (d deniedFS) Open(name string) (fs.File, error) {
	if name == d.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}

// TestCPUCollectParity runs the legacy SysfsCPU backend and the collect
// sensor, through the bridge and CPU adapter, over the same sysfs fixtures.
// Values, failed signals, and verdicts must match. Remove with SysfsCPU.
func TestCPUCollectParity(t *testing.T) {
	files := func(possible, online, node string) fstest.MapFS {
		out := fstest.MapFS{}
		for path, data := range map[string]string{
			cpuPossiblePath: possible, cpuOnlinePath: online, numaNodeOnlinePath: node,
		} {
			if data != "" {
				out[strings.TrimPrefix(path, "/")] = &fstest.MapFile{Data: []byte(data)}
			}
		}
		return out
	}
	tests := []struct {
		fsys fs.FS
		name string
	}{
		{name: "healthy", fsys: files("0-143\n", "0-143\n", "0-33\n")},
		{name: "offline CPU and wrong NUMA count", fsys: files("0-143\n", "0-141\n", "0-1\n")},
		{name: "sparse lists", fsys: files("0-3,8-11", "0-3,8-11", "0,2")},
		{name: "missing NUMA list", fsys: files("0-143", "0-143", "")},
		{name: "malformed online mask", fsys: files("0-143", "0-3,2", "0-33")},
		{name: "empty possible mask", fsys: files(" \n", "0-143", "0-33")},
		{name: "all missing", fsys: fstest.MapFS{}},
		{name: "permission denied", fsys: deniedFS{FS: files("0-143", "0-143", "0-33"), path: strings.TrimPrefix(cpuPossiblePath, "/")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			legacy := legacyCPU(ctx, t, tt.fsys)
			bridged := collectCPU(ctx, t, tt.fsys)

			if legacy.PossibleCPUCount != bridged.PossibleCPUCount || legacy.OnlineCPUCount != bridged.OnlineCPUCount ||
				legacy.NUMANodeCount != bridged.NUMANodeCount {
				t.Errorf("values differ:\n legacy=%+v\n collect=%+v", legacy, bridged)
			}
			if l, c := failedCPUSignals(legacy), failedCPUSignals(bridged); !slices.Equal(l, c) {
				t.Errorf("failed signals differ: legacy=%v collect=%v", l, c)
			}
			if l, c := cpuVerdicts(t, evaluateCPU(t, legacy)), cpuVerdicts(t, evaluateCPU(t, bridged)); !reflect.DeepEqual(l, c) {
				t.Errorf("verdicts differ: legacy=%v collect=%v", l, c)
			}
		})
	}
}

func legacyCPU(ctx context.Context, t *testing.T, fsys fs.FS) cpu.CPUMeasurement {
	t.Helper()
	sensor := NewCPUSensor(&sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
		return fs.ReadFile(fsys, strings.TrimPrefix(path, "/"))
	}})
	measurements, err := sensor.Measure(ctx)
	if err != nil {
		measurements = sensor.Errored(err)
	}
	return onlyCPU(t, measurements)
}

func collectCPU(ctx context.Context, t *testing.T, fsys fs.FS) cpu.CPUMeasurement {
	t.Helper()
	cycle, err := collect.NewCycle("parity", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	adapter := collectadapters.CPU()
	runner := collectbridge.NewRunner(cycle)
	if err = runner.Register(cpusensor.New(fsys), slices.Sorted(maps.Keys(adapter.Signals()))...); err != nil {
		t.Fatal(err)
	}
	runner.Collect()
	measurements, err := runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: slices.Sorted(maps.Keys(adapter.Signals())), Build: adapter.Build, Errored: adapter.Errored,
	}).Measure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return onlyCPU(t, measurements)
}

func onlyCPU(t *testing.T, measurements []health.Measurement) cpu.CPUMeasurement {
	t.Helper()
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1: %v", len(measurements), measurements)
	}
	m, ok := measurements[0].(cpu.CPUMeasurement)
	if !ok {
		t.Fatalf("got %T, want cpu.CPUMeasurement", measurements[0])
	}
	return m
}

func failedCPUSignals(m cpu.CPUMeasurement) []string {
	var out []string
	for _, e := range m.Errors() {
		out = append(out, e.FailedSignals()...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TestCPUCollectTimeoutParity stalls both paths' sysfs reads past their
// timeout. Both must fail every CPU check with a retry-collection action.
func TestCPUCollectTimeoutParity(t *testing.T) {
	const timeout = 10 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	legacy, err := CollectWithOptions(cpuCatalog(t), CollectionOptions{Timeout: timeout},
		NewCPUSensor(&sysfsCPUBackend{readFile: func(string) ([]byte, error) {
			<-release
			return nil, fs.ErrNotExist
		}}))
	if err != nil {
		t.Fatal(err)
	}

	cycle, err := collect.NewCycleWithTimeout("parity", time.Now(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	adapter := collectadapters.CPU()
	runner := collectbridge.NewRunner(cycle)
	if err = runner.Register(cpusensor.New(blockingFS(release)), slices.Sorted(maps.Keys(adapter.Signals()))...); err != nil {
		t.Fatal(err)
	}
	runner.Collect()
	measurements, err := runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: slices.Sorted(maps.Keys(adapter.Signals())), Build: adapter.Build, Errored: adapter.Errored,
	}).Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bridged := evaluateCPU(t, onlyCPU(t, measurements))

	l, c := cpuVerdicts(t, legacy), cpuVerdicts(t, bridged)
	if !reflect.DeepEqual(l, c) {
		t.Errorf("verdicts differ: legacy=%v collect=%v", l, c)
	}
	for id, v := range c {
		if v.severity != health.Critical || !v.action.Equal(health.Drain(health.RemediationActionRetryCollection)) {
			t.Errorf("%s = %+v, want CRITICAL with retry collection", id, v)
		}
	}
}

// blockingFS stalls every open until release is closed.
type blockingFS <-chan struct{}

func (b blockingFS) Open(string) (fs.File, error) {
	<-b
	return nil, fs.ErrNotExist
}

func cpuCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, check := range []health.CheckDefinition{cpu.CPUCount(), cpu.NUMANodeCount()} {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

func evaluateCPU(t *testing.T, m cpu.CPUMeasurement) health.NodeVerdict {
	t.Helper()
	verdict, err := cpuCatalog(t).EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

type cpuVerdict struct {
	action   health.RecommendedAction
	severity health.Severity
}

func cpuVerdicts(t *testing.T, verdict health.NodeVerdict) map[string]cpuVerdict {
	t.Helper()
	out := make(map[string]cpuVerdict)
	for _, result := range verdict.CheckResults {
		out[result.CheckID] = cpuVerdict{action: result.RecommendedAction, severity: result.Severity}
	}
	if len(out) != 2 {
		t.Fatalf("evaluated checks %v, want cpu_count and numa_node_count", slices.Sorted(maps.Keys(out)))
	}
	return out
}
