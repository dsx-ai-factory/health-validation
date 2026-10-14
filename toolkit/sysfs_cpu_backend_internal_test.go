// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
)

func TestSysfsCPUBackendMeasureCPU(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var paths []string
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			paths = append(paths, path)
			switch path {
			case cpuPossiblePath:
				return []byte("0-143\n"), nil
			case cpuOnlinePath:
				return []byte("0-142\n"), nil
			case numaNodeOnlinePath:
				return []byte("0-33\n"), nil
			default:
				t.Fatalf("unexpected path %q", path)
				return nil, nil
			}
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got, want := paths, []string{cpuPossiblePath, cpuOnlinePath, numaNodeOnlinePath}; !slices.Equal(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
		if measurement.PossibleCPUCount != 144 {
			t.Errorf("PossibleCPUCount = %d, want 144", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 143 {
			t.Errorf("OnlineCPUCount = %d, want 143", measurement.OnlineCPUCount)
		}
		if measurement.NUMANodeCount != 34 {
			t.Errorf("NUMANodeCount = %d, want 34", measurement.NUMANodeCount)
		}
		if got := len(measurement.Errors()); got != 0 {
			t.Errorf("Errors length = %d, want 0", got)
		}
	})

	t.Run("equal lists", func(t *testing.T) {
		var paths []string
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			paths = append(paths, path)
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got, want := paths, []string{cpuPossiblePath, cpuOnlinePath, numaNodeOnlinePath}; !slices.Equal(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
		if measurement.PossibleCPUCount != 4 || measurement.OnlineCPUCount != 4 {
			t.Errorf(
				"counts = possible:%d online:%d, want possible:4 online:4",
				measurement.PossibleCPUCount,
				measurement.OnlineCPUCount,
			)
		}
		if got := len(measurement.Errors()); got != 0 {
			t.Errorf("Errors length = %d, want 0", got)
		}
	})

	t.Run("sparse lists", func(t *testing.T) {
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == cpuPossiblePath {
				return []byte("0-3,8-11,14,17\n"), nil
			}
			return []byte("0-2,8-10,14\n"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if measurement.PossibleCPUCount != 10 {
			t.Errorf("PossibleCPUCount = %d, want 10", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 7 {
			t.Errorf("OnlineCPUCount = %d, want 7", measurement.OnlineCPUCount)
		}
	})

	t.Run("possible read failure preserves online count", func(t *testing.T) {
		cause := errors.New("permission denied")
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == cpuPossiblePath {
				return nil, cause
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 0 {
			t.Errorf("PossibleCPUCount = %d, want zero-value signal", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 4 {
			t.Errorf("OnlineCPUCount = %d, want 4", measurement.OnlineCPUCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			cause,
			cpu.SignalPossibleCPUCount,
			"read CPU possible list /sys/devices/system/cpu/possible",
		)
	})

	t.Run("possible parse failure preserves online count", func(t *testing.T) {
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == cpuPossiblePath {
				return []byte("0-"), nil
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 0 {
			t.Errorf("PossibleCPUCount = %d, want zero-value signal", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 4 {
			t.Errorf("OnlineCPUCount = %d, want 4", measurement.OnlineCPUCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			nil,
			cpu.SignalPossibleCPUCount,
			"parse CPU possible list /sys/devices/system/cpu/possible",
		)
	})

	t.Run("online read failure preserves possible count", func(t *testing.T) {
		cause := errors.New("permission denied")
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == cpuOnlinePath {
				return nil, cause
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 4 {
			t.Errorf("PossibleCPUCount = %d, want 4", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 0 {
			t.Errorf("OnlineCPUCount = %d, want zero-value signal", measurement.OnlineCPUCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			cause,
			cpu.SignalOnlineCPUCount,
			"read CPU online list /sys/devices/system/cpu/online",
		)
	})

	t.Run("online parse failure preserves possible count", func(t *testing.T) {
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == cpuOnlinePath {
				return []byte("\n"), nil
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 4 {
			t.Errorf("PossibleCPUCount = %d, want 4", measurement.PossibleCPUCount)
		}
		if measurement.OnlineCPUCount != 0 {
			t.Errorf("OnlineCPUCount = %d, want zero-value signal", measurement.OnlineCPUCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			nil,
			cpu.SignalOnlineCPUCount,
			"parse CPU online list /sys/devices/system/cpu/online",
		)
	})

	t.Run("numa read failure preserves cpu counts", func(t *testing.T) {
		cause := errors.New("permission denied")
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == numaNodeOnlinePath {
				return nil, cause
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 4 || measurement.OnlineCPUCount != 4 {
			t.Errorf(
				"counts = possible:%d online:%d, want possible:4 online:4",
				measurement.PossibleCPUCount,
				measurement.OnlineCPUCount,
			)
		}
		if measurement.NUMANodeCount != 0 {
			t.Errorf("NUMANodeCount = %d, want zero-value signal", measurement.NUMANodeCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			cause,
			cpu.SignalNUMANodeCount,
			"read NUMA node online list /sys/devices/system/node/online",
		)
	})

	t.Run("numa parse failure preserves cpu counts", func(t *testing.T) {
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			if path == numaNodeOnlinePath {
				return []byte("\n"), nil
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		if measurement.PossibleCPUCount != 4 || measurement.OnlineCPUCount != 4 {
			t.Errorf(
				"counts = possible:%d online:%d, want possible:4 online:4",
				measurement.PossibleCPUCount,
				measurement.OnlineCPUCount,
			)
		}
		if measurement.NUMANodeCount != 0 {
			t.Errorf("NUMANodeCount = %d, want zero-value signal", measurement.NUMANodeCount)
		}
		assertCPUCollectionError(
			t,
			measurement,
			1,
			0,
			nil,
			cpu.SignalNUMANodeCount,
			"parse NUMA node online list /sys/devices/system/node/online",
		)
	})

	t.Run("independent failures accumulate", func(t *testing.T) {
		possibleCause := errors.New("possible list unavailable")
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			switch path {
			case cpuPossiblePath:
				return nil, possibleCause
			case cpuOnlinePath:
				return []byte("0-"), nil
			default:
				return []byte("0-3"), nil
			}
		}}

		measurement, err := backend.MeasureCPU(context.Background())
		if err != nil {
			t.Fatalf("MeasureCPU returned top-level error: %v", err)
		}
		assertCPUCollectionError(
			t,
			measurement,
			2,
			0,
			possibleCause,
			cpu.SignalPossibleCPUCount,
			"read CPU possible list",
		)
		assertCPUCollectionError(
			t,
			measurement,
			2,
			1,
			nil,
			cpu.SignalOnlineCPUCount,
			"parse CPU online list",
		)
	})

	t.Run("canceled before reads", func(t *testing.T) {
		called := false
		backend := &sysfsCPUBackend{readFile: func(string) ([]byte, error) {
			called = true
			return []byte("0-3"), nil
		}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		measurement, err := backend.MeasureCPU(ctx)
		if called {
			t.Error("file reader called for canceled context")
		}
		assertCanceledCPUCollection(t, measurement, err)
	})

	t.Run("canceled during possible read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var paths []string
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			paths = append(paths, path)
			cancel()
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(ctx)
		if got, want := paths, []string{cpuPossiblePath}; !slices.Equal(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
		assertCanceledCPUCollection(t, measurement, err)
	})

	t.Run("canceled during online read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var paths []string
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			paths = append(paths, path)
			if path == cpuOnlinePath {
				cancel()
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(ctx)
		if got, want := paths, []string{cpuPossiblePath, cpuOnlinePath}; !slices.Equal(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
		assertCanceledCPUCollection(t, measurement, err)
	})

	t.Run("canceled during numa node read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var paths []string
		backend := &sysfsCPUBackend{readFile: func(path string) ([]byte, error) {
			paths = append(paths, path)
			if path == numaNodeOnlinePath {
				cancel()
			}
			return []byte("0-3"), nil
		}}

		measurement, err := backend.MeasureCPU(ctx)
		if got, want := paths, []string{cpuPossiblePath, cpuOnlinePath, numaNodeOnlinePath}; !slices.Equal(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
		assertCanceledCPUCollection(t, measurement, err)
	})
}

func TestSysfsCPUUsesProductionPaths(t *testing.T) {
	backend, ok := SysfsCPU().(*sysfsCPUBackend)
	if !ok {
		t.Fatalf("SysfsCPU() returned %T, want *sysfsCPUBackend", backend)
		return
	}

	var paths []string
	backend.readFile = func(path string) ([]byte, error) {
		paths = append(paths, path)
		switch path {
		case cpuPossiblePath:
			return []byte("0-7"), nil
		case numaNodeOnlinePath:
			return []byte("0-1"), nil
		default:
			return []byte("0-6"), nil
		}
	}

	measurement, err := backend.MeasureCPU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths, []string{cpuPossiblePath, cpuOnlinePath, numaNodeOnlinePath}; !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
	if measurement.PossibleCPUCount != 8 {
		t.Errorf("PossibleCPUCount = %d, want 8", measurement.PossibleCPUCount)
	}
	if measurement.OnlineCPUCount != 7 {
		t.Errorf("OnlineCPUCount = %d, want 7", measurement.OnlineCPUCount)
	}
	if measurement.NUMANodeCount != 2 {
		t.Errorf("NUMANodeCount = %d, want 2", measurement.NUMANodeCount)
	}
}

func assertCPUCollectionError(
	t *testing.T,
	measurement cpu.CPUMeasurement,
	wantErrorCount int,
	index int,
	wantCause error,
	wantSignal string,
	wantMessage string,
) {
	t.Helper()

	errs := measurement.Errors()
	if len(errs) != wantErrorCount {
		t.Fatalf("Errors length = %d, want %d", len(errs), wantErrorCount)
	}
	if wantCause != nil && !errors.Is(errs[index].Err(), wantCause) {
		t.Errorf("error = %v, want cause %v", errs[index].Err(), wantCause)
	}
	if !strings.Contains(errs[index].Err().Error(), wantMessage) {
		t.Errorf("error = %v, want message containing %q", errs[index].Err(), wantMessage)
	}
	if got, want := errs[index].FailedSignals(), []string{wantSignal}; !slices.Equal(got, want) {
		t.Errorf("FailedSignals = %v, want %v", got, want)
	}
}

func assertCanceledCPUCollection(t *testing.T, measurement cpu.CPUMeasurement, err error) {
	t.Helper()

	if measurement.PossibleCPUCount != 0 ||
		measurement.OnlineCPUCount != 0 ||
		measurement.NUMANodeCount != 0 ||
		len(measurement.Errors()) != 0 {
		t.Errorf(
			"measurement = %+v with %d errors, want zero-value measurement",
			measurement,
			len(measurement.Errors()),
		)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if err == nil || !strings.Contains(err.Error(), "collect CPU availability") {
		t.Errorf("error = %v, want collection-stage context", err)
	}
}
