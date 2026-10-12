package nvml

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type processDevice struct {
	device.Device
	onRead  func()
	members []nvml.ProcessInfo
	calls   int
	ret     nvml.Return
}

func (d *processDevice) GetComputeRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	d.calls++
	if d.onRead != nil {
		d.onRead()
	}
	return d.members, d.ret
}

func gpuProcStat(start uint64) []byte {
	return []byte(fmt.Sprintf("42 (truncated-name) D %s%d 0\n", strings.Repeat("0 ", 18), start))
}

func gpuProcFS() fstest.MapFS {
	return fstest.MapFS{"proc/sys/kernel/random/boot_id": &fstest.MapFile{Data: []byte("01234567-89ab-cdef-0123-456789abcdef")}, "proc/42/stat": &fstest.MapFile{Data: gpuProcStat(500)}, "proc/42/comm": &fstest.MapFile{Data: []byte("truncated-name\n")}}
}

func measureProcessFixture(dev *processDevice, fsys fs.FS) *observation.ObservationBatch {
	want := make(map[string]bool)
	for _, definition := range processSignals() {
		want[definition.ID()] = true
	}
	batch := &observation.ObservationBatch{}
	measureProcessesFS(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("GPU-1"), dev, want, fsys)
	return batch
}

func TestGPUProcessMembershipAndMemory(t *testing.T) {
	dev := &processDevice{members: []nvml.ProcessInfo{{Pid: 42, UsedGpuMemory: 1 << 63}}, ret: nvml.SUCCESS}
	batch := measureProcessFixture(dev, gpuProcFS())
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Observations) != 5 || dev.calls != 1 {
		t.Fatal("membership acquisition incorrect")
	}
	for _, current := range batch.Observations {
		if current.SignalId == gpusignals.ProcessCount.ID() {
			continue
		}
		ref := current.GetTarget().GetGpuProcess()
		if ref.GetGpu().GetUuid() != "GPU-1" || ref.GetProcess().GetStartTimeTicks() != 500 {
			t.Fatal("GPU process identity lost")
		}
		if current.SignalId == gpusignals.ProcessMemoryUsed.ID() && current.GetValue().GetUintValue() != 1<<63 {
			t.Fatal("unsigned memory overflow")
		}
	}
}

func TestGPUProcessFailuresAndNoTargets(t *testing.T) {
	dev := &processDevice{ret: nvml.ERROR_NOT_SUPPORTED}
	batch := measureProcessFixture(dev, gpuProcFS())
	for _, current := range batch.Observations {
		if current.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnsupported {
			t.Fatal("unsupported membership suppressed")
		}
	}
	dev = &processDevice{ret: nvml.SUCCESS}
	batch = measureProcessFixture(dev, gpuProcFS())
	for _, current := range batch.Observations {
		if current.SignalId != gpusignals.ProcessCount.ID() && current.GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
			t.Fatal("empty membership missing no targets")
		}
	}
	for _, mutation := range []string{"reuse", "disappear", "comm"} {
		t.Run(mutation, func(t *testing.T) {
			fsys := gpuProcFS()
			dev := &processDevice{ret: nvml.SUCCESS, members: []nvml.ProcessInfo{{Pid: 42, UsedGpuMemory: ^uint64(0)}}, onRead: func() {
				switch mutation {
				case "reuse":
					fsys["proc/42/stat"].Data = gpuProcStat(600)
				case "disappear":
					delete(fsys, "proc/42/stat")
				case "comm":
					delete(fsys, "proc/42/comm")
				}
			}}
			batch := measureProcessFixture(dev, fsys)
			if err := observation.ValidateBatch(batch); err != nil {
				t.Fatal(err)
			}
			if batch.Observations[0].GetValue().GetUintValue() != 1 {
				t.Fatal("native count erased by metadata failure")
			}
			for _, current := range batch.Observations[1:] {
				if mutation != "comm" && current.GetCollectionError() == nil {
					t.Fatal("stale PID identity emitted")
				}
				if mutation == "comm" && current.SignalId == gpusignals.ProcessMember.ID() && !current.GetValue().GetBoolValue() {
					t.Fatal("membership erased by failed optional comm")
				}
				if current.SignalId == gpusignals.ProcessMemoryUsed.ID() && current.GetValue() != nil {
					t.Fatal("memory sentinel emitted")
				}
			}
		})
	}
	dev = &processDevice{}
	measureProcessesFS(context.Background(), &observation.ObservationBatch{}, timestamppb.Now(), observation.GPUTarget("GPU-1"), dev, map[string]bool{}, nil)
	if dev.calls != 0 {
		t.Fatal("empty selection read NVML")
	}
}

type gpuProcessDeniedFS struct{ files fstest.MapFS }

func (f gpuProcessDeniedFS) Open(path string) (fs.File, error) {
	if path == "proc/42/stat" {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return f.files.Open(path)
}

func TestGPUProcessPermissionAndMalformed(t *testing.T) {
	malformed := gpuProcFS()
	malformed["proc/42/stat"].Data = []byte("bad stat")
	for _, test := range []struct {
		fsys     fs.FS
		category observation.CollectionErrorCategory
	}{
		{gpuProcessDeniedFS{gpuProcFS()}, observation.CollectionErrorCategoryPermissionDenied},
		{malformed, observation.CollectionErrorCategoryParseError},
	} {
		dev := &processDevice{ret: nvml.SUCCESS, members: []nvml.ProcessInfo{{Pid: 42, UsedGpuMemory: 128}}}
		batch := measureProcessFixture(dev, test.fsys)
		if batch.Observations[0].GetValue().GetUintValue() != 1 {
			t.Fatal("metadata failure erased count")
		}
		for _, current := range batch.Observations[1:] {
			if current.GetCollectionError().GetCategory() != test.category {
				t.Fatalf("category = %v", current.GetCollectionError().GetCategory())
			}
		}
	}
}

func TestGPUProcessCmdlineOptInAndDState(t *testing.T) {
	for _, state := range []string{"D", "R"} {
		fsys := gpuProcFS()
		fsys["proc/42/stat"].Data = []byte(strings.Replace(string(gpuProcStat(500)), ") D ", ") "+state+" ", 1))
		fsys["proc/42/cmdline"] = &fstest.MapFile{Data: []byte(strings.Repeat("x", 5000))}
		dev := &processDevice{ret: nvml.SUCCESS, members: []nvml.ProcessInfo{{Pid: 42}}}
		want := map[string]bool{gpusignals.ProcessCmdline.ID(): true, gpusignals.ProcessCmdlineTruncated.ID(): true}
		batch := &observation.ObservationBatch{}
		measureProcessesWithDiagnostics(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("GPU-1"), dev, want, fsys, false)
		if dev.calls != 0 || len(batch.Observations) != 0 {
			t.Fatal("diagnostics acquired without opt-in")
		}
		measureProcessesWithDiagnostics(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("GPU-1"), dev, want, fsys, true)
		if len(batch.Observations) != 2 {
			t.Fatal("diagnostic outcomes missing")
		}
		if state == "D" {
			for _, current := range batch.Observations {
				if current.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatal("D-state cmdline was read")
				}
			}
		} else {
			if len(batch.Observations[0].GetValue().GetStringValue()) != 4096 || !batch.Observations[1].GetValue().GetBoolValue() {
				t.Fatal("cmdline bound/truncation lost")
			}
		}
	}
}
