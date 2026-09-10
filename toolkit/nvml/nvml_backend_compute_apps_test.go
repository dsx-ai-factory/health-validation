//go:build linux && !android

package nvml_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

func computeAppsDevice(infos []gonvml.ProcessInfo, ret gonvml.Return) *mock.Device {
	dev := powerLimitDevice(1_200_000, 1_200_000)
	dev.GetComputeRunningProcessesFunc = func() ([]gonvml.ProcessInfo, gonvml.Return) {
		return infos, ret
	}
	return dev
}

func TestMeasureGPUs_ComputeProcessesEmpty(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(computeAppsDevice(nil, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.ComputeProcessCount != 0 {
		t.Errorf("ComputeProcessCount = %d, want 0", m.ComputeProcessCount)
	}
	if m.ComputeProcessCmdlines != "" {
		t.Errorf("ComputeProcessCmdlines = %q, want empty", m.ComputeProcessCmdlines)
	}
	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
}

func TestMeasureGPUs_ComputeProcessesSkipsPidZero(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(computeAppsDevice([]gonvml.ProcessInfo{
		{Pid: 0},
		{Pid: 1 << 30},
	}, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.ComputeProcessCount != 1 {
		t.Errorf("ComputeProcessCount = %d, want 1 (pid 0 skipped)", m.ComputeProcessCount)
	}
	if m.ComputeProcessCmdlines != "pid="+strconv.FormatUint(1<<30, 10) {
		t.Errorf("ComputeProcessCmdlines = %q, want pid-only for unreadable /proc", m.ComputeProcessCmdlines)
	}
}

func TestMeasureGPUs_ComputeProcessesJoinsWithNewline(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(computeAppsDevice([]gonvml.ProcessInfo{
		{Pid: 1 << 30},
		{Pid: 1<<30 + 1},
	}, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.ComputeProcessCount != 2 {
		t.Errorf("ComputeProcessCount = %d, want 2", m.ComputeProcessCount)
	}
	want := "pid=" + strconv.FormatUint(1<<30, 10) + "\npid=" + strconv.FormatUint(1<<30+1, 10)
	if m.ComputeProcessCmdlines != want {
		t.Errorf("ComputeProcessCmdlines = %q, want %q", m.ComputeProcessCmdlines, want)
	}
}

func TestMeasureGPUs_ComputeProcessesReadsProcCmdline(t *testing.T) {
	pid := uint32(os.Getpid())
	b := nvml.NewWithLibrary(pstateLib(computeAppsDevice([]gonvml.ProcessInfo{{Pid: pid}}, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.ComputeProcessCount != 1 {
		t.Errorf("ComputeProcessCount = %d, want 1", m.ComputeProcessCount)
	}
	wantPrefix := "pid=" + strconv.FormatUint(uint64(pid), 10) + " \""
	if !strings.HasPrefix(m.ComputeProcessCmdlines, wantPrefix) {
		t.Fatalf("ComputeProcessCmdlines = %q, want prefix %q", m.ComputeProcessCmdlines, wantPrefix)
	}
	base := filepath.Base(os.Args[0])
	if !strings.Contains(m.ComputeProcessCmdlines, base) {
		t.Errorf("ComputeProcessCmdlines = %q, want %q from /proc cmdline", m.ComputeProcessCmdlines, base)
	}
}

func TestMeasureGPUs_ComputeProcessQueryFailureFlagsSignals(t *testing.T) {
	for _, ret := range []gonvml.Return{gonvml.ERROR_UNKNOWN, gonvml.ERROR_NOT_SUPPORTED} {
		t.Run(ret.String(), func(t *testing.T) {
			b := nvml.NewWithLibrary(pstateLib(computeAppsDevice(nil, ret)))
			t.Cleanup(func() { b.Close() })

			ms, err := b.MeasureGPUs()
			if err != nil {
				t.Fatalf("MeasureGPUs: %v", err)
			}
			m := expectGPUMeasurement(t, ms, 0)
			errs := m.Errors()
			if len(errs) != 1 {
				t.Fatalf("Errors() = %v, want 1 entry", errs)
			}
			want := []string{gpu.SignalComputeProcessCount, gpu.SignalComputeProcessCmdlines}
			if got := errs[0].FailedSignals(); !slices.Equal(got, want) {
				t.Errorf("FailedSignals = %v, want %v", got, want)
			}
			nvmlErr, ok := errs[0].Err().(*nvml.Error)
			if !ok {
				t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
			}
			if nvmlErr.Op != "DeviceGetComputeRunningProcesses" {
				t.Errorf("Op = %q, want DeviceGetComputeRunningProcesses", nvmlErr.Op)
			}
			if m.ComputeProcessCount != 0 || m.ComputeProcessCmdlines != "" {
				t.Errorf("count/cmdlines = %d/%q, want zero on query failure", m.ComputeProcessCount, m.ComputeProcessCmdlines)
			}
		})
	}
}
