// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// stallSensor serves part of gpuSpySensor's GPU signals under one native
// library's cycle key, as the stock DCGM and NVML sensors do. A non-nil
// release parks the read until it is closed.
type stallSensor struct {
	release chan struct{}
	id      string
	source  string
	signals collect.Signals
}

func (s stallSensor) ID() string               { return s.id }
func (s stallSensor) Signals() collect.Signals { return s.signals }

func (s stallSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	return collect.Run(ctx, collect.NewOp(opts...).Cycle(), s.source, func(context.Context) (*observation.ObservationBatch, error) {
		if s.release != nil {
			<-s.release
		}
		return gpuSpySensor{called: new(bool)}.Measure(ctx, opts...)
	})
}

// countingReader is fakeHost's inventory under NVML's cycle key, counting
// reads.
type countingReader struct {
	reads *atomic.Int32
}

func (countingReader) ID() string { return "nvml" }

func (r countingReader) Read(ctx context.Context) (*observation.GPUInventorySnapshot, error) {
	r.reads.Add(1)
	return inventoryReader{gpus: 4, model: "NVIDIA GB200"}.Read(ctx)
}

// sharedInventoryHost serves the given GPU sensors and one inventory for the
// whole run, as the production host does.
type sharedInventoryHost struct {
	inventory *gpuinventory.Inventory
	fakeHost
	gpu []collect.Sensor
}

func (h sharedInventoryHost) gpuSensors() []collect.Sensor          { return h.gpu }
func (h sharedInventoryHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }

// A stalled DCGM or NVML read fails only the checks that library feeds, and
// the node GPU count reuses discovery's snapshot instead of reading NVML again.
func TestStalledNativeSourceFailsOnlyItsChecks(t *testing.T) {
	fedBy := map[string][]string{
		"dcgm": {"gpu_average_power_draw", "gpu_power_limit", "gpu_power_state", "gpu_sensor_throttle"},
		"nvml": {"gpu_compute_apps"},
	}
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("collection_timeout_seconds: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for stalled, stalledChecks := range fedBy {
		t.Run(stalled, func(t *testing.T) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			sensor := func(id, source string, signals collect.Signals) stallSensor {
				s := stallSensor{id: id, source: source, signals: signals}
				if source == stalled {
					s.release = release
				}
				return s
			}
			var reads atomic.Int32
			a := cadenceRunApp()
			a.host = sharedInventoryHost{
				fakeHost:  fakeHost{b: spyBackend{called: new(bool)}},
				inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return countingReader{reads: &reads}, nil }),
				gpu:       []collect.Sensor{sensor("gpu.dcgm", "dcgm", dcgmGPUSignals), sensor("gpu.nvml", "nvml", processGPUSignals)},
			}

			var stdout, stderr bytes.Buffer
			if code := a.runCommand([]string{"--node-id", testNodeID, "--config", config}, &stdout, &stderr); code != exitUnhealthy {
				t.Fatalf("exit = %d, want exitUnhealthy; stderr = %q", code, stderr.String())
			}
			var v health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
				t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
			}
			if n := reads.Load(); n != 1 {
				t.Errorf("inventory reads = %d, want 1", n)
			}
			retry := health.Drain(health.RemediationActionRetryCollection)
			failed := make(map[string]bool)
			for _, r := range v.CheckResults {
				switch {
				case slices.Contains(stalledChecks, r.CheckID):
					failed[r.CheckID] = true
					if r.Severity != health.Unknown || !r.RecommendedAction.Equal(retry) {
						t.Errorf("%s = %s %+v, want UNKNOWN retry_collection", r.CheckID, r.Severity, r.RecommendedAction)
					}
				case r.CheckID == "gpu_count" || slices.Contains(slices.Concat(fedBy["dcgm"], fedBy["nvml"]), r.CheckID):
					if r.Severity != health.Pass {
						t.Errorf("%s = %s %v, want PASS", r.CheckID, r.Severity, r.Messages)
					}
				}
			}
			for _, id := range stalledChecks {
				if !failed[id] {
					t.Errorf("%s has no result", id)
				}
			}
		})
	}
}

// gatedReader holds a process-wide gate for each read, as the NVML source's
// native-call gate does, and refuses a read while another holds it. The first
// read parks until release; done closes once it has returned the gate.
type gatedReader struct {
	gate    *sync.Mutex
	entered *atomic.Int32
	release chan struct{}
	done    chan struct{}
}

func (gatedReader) ID() string { return "nvml" }

func (r gatedReader) Read(ctx context.Context) (*observation.GPUInventorySnapshot, error) {
	if !r.gate.TryLock() {
		return nil, fmt.Errorf("read GPU inventory: %w", gpuinventory.ErrSourceBusy)
	}
	first := r.entered.Add(1) == 1
	if first {
		defer close(r.done)
	}
	defer r.gate.Unlock()
	if first {
		<-r.release
	}
	return inventoryReader{gpus: 4, model: "NVIDIA GB200"}.Read(ctx)
}

// A timeout stops waiting for a native read but not the read itself, and Run
// leaves the host open, so the next run on the same App can find the source
// still busy. That run reports UNKNOWN with retry_collection without entering
// the library again, and a run after the read returns passes.
func TestNextRunFindsTimedOutReadBusy(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("collection_timeout_seconds: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var entered atomic.Int32
	reader := gatedReader{gate: new(sync.Mutex), entered: &entered, release: make(chan struct{}), done: make(chan struct{})}
	release := sync.OnceFunc(func() { close(reader.release) })
	t.Cleanup(release)
	host := fakeHost{b: spyBackend{called: new(bool)}}
	a := cadenceRunApp()
	a.host = sharedInventoryHost{
		fakeHost:  host,
		inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return reader, nil }),
		gpu:       host.gpuSensors(),
	}
	run := func(want int) health.NodeVerdict {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := a.runCommand([]string{"--node-id", testNodeID, "--config", config}, &stdout, &stderr); code != want {
			t.Fatalf("exit = %d, want %d; stderr = %q", code, want, stderr.String())
		}
		var v health.NodeVerdict
		if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
			t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
		}
		return v
	}
	discovery := func(run string, v health.NodeVerdict, severity health.Severity, action health.RecommendedAction) {
		t.Helper()
		if len(v.CheckResults) != 1 || v.CheckResults[0].CheckID != "architecture_discovery" ||
			v.OverallSeverity != severity || !v.RecommendedAction.Equal(action) {
			t.Fatalf("%s verdict = %+v, want one %s architecture_discovery result recommending %+v", run, v, severity, action)
		}
	}

	// A discovery timeout is not a busy source, so it keeps the generic drain.
	discovery("run 1", run(exitUnhealthy), health.Critical, health.GenericDrain())
	discovery("run 2", run(exitUnhealthy), health.Unknown, health.Drain(health.RemediationActionRetryCollection))
	if n := entered.Load(); n != 1 {
		t.Fatalf("native reads entered = %d, want 1", n)
	}

	release()
	<-reader.done
	if v := run(exitOK); v.OverallSeverity != health.Pass {
		t.Errorf("run 3 severity = %s, want PASS", v.OverallSeverity)
	}
}
