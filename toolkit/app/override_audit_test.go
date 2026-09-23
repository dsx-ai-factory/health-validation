package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// datagramRecorder stands in for the journald socket: each Write is one
// entry. With writeErr set, the Write at index failAt fails.
type datagramRecorder struct {
	deadline  time.Time
	writeErr  error
	datagrams [][]byte
	failAt    int
}

func (r *datagramRecorder) Write(b []byte) (int, error) {
	if r.writeErr != nil && len(r.datagrams) == r.failAt {
		return 0, r.writeErr
	}
	r.datagrams = append(r.datagrams, append([]byte(nil), b...))
	return len(b), nil
}

func (r *datagramRecorder) SetWriteDeadline(t time.Time) error {
	r.deadline = t
	return nil
}

func (r *datagramRecorder) Close() error { return nil }

// parseDatagram decodes a journal entry written in the NAME=value form. Audit
// values never contain newlines, so a length-framed field fails the test.
func parseDatagram(t *testing.T, b []byte) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("journal field %q is not NAME=value", line)
		}
		fields[name] = value
	}
	return fields
}

func TestOverrideRecords(t *testing.T) {
	custom := customCheck()
	custom.Provenance = health.Private
	cat := catalog.New()
	if err := cat.Register(custom); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		override catalog.Override
		arch     string
		want     overrideRecord
	}{
		{
			arch:     "GB200",
			override: catalog.Override{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: 1000},
			want:     overrideRecord{originalValue: float64(1200), value: 1000, checkID: "gpu_power_limit", thresholdName: "expected_power_limit_w", provenance: health.NVIDIA},
		},
		{
			arch:     "GB300",
			override: catalog.Override{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: 1000},
			want:     overrideRecord{originalValue: float64(1400), value: 1000, checkID: "gpu_power_limit", thresholdName: "expected_power_limit_w", provenance: health.NVIDIA},
		},
		{
			arch:     "GB200",
			override: catalog.Override{CheckID: custom.ID, ThresholdName: "max_count", Value: int64(3)},
			want:     overrideRecord{originalValue: int64(8), value: int64(3), checkID: custom.ID, thresholdName: "max_count", provenance: health.Private},
		},
	}
	for _, tt := range tests {
		t.Run(tt.arch+"/"+tt.override.CheckID, func(t *testing.T) {
			got, err := overrideRecords(cat, []catalog.Override{tt.override}, tt.arch)
			if err != nil {
				t.Fatalf("overrideRecords() error = %v", err)
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("overrideRecords() = %+v, want [%+v]", got, tt.want)
			}
		})
	}
}

func TestWriteOverrideAudit(t *testing.T) {
	records := []overrideRecord{
		{originalValue: int64(4), value: int64(8), checkID: "gpu_count", thresholdName: "expected_gpu_count", provenance: health.NVIDIA},
		{originalValue: float64(1200), value: 1000, checkID: "gpu_power_limit", thresholdName: "expected_power_limit_w", provenance: health.Private},
	}
	verdict := health.NodeVerdict{
		Timestamp:      time.Date(2026, 9, 22, 7, 30, 0, 5, time.FixedZone("CDT", -5*60*60)),
		TraceID:        "trace-1",
		CatalogVersion: "v0.2.0",
	}
	var rec datagramRecorder

	if err := writeOverrideAudit(&rec, records, verdict, "node-1", "health-validation"); err != nil {
		t.Fatalf("writeOverrideAudit() error = %v", err)
	}

	if rec.deadline.IsZero() {
		t.Error("write deadline not set")
	}
	shared := map[string]string{
		"MESSAGE_ID":        overrideAuditMessageID,
		"PRIORITY":          "5",
		"SYSLOG_IDENTIFIER": "health-validation",
		"TIMESTAMP":         "2026-09-22T12:30:00.000000005Z",
		"TRACE_ID":          "trace-1",
		"NODE_ID":           "node-1",
		"CATALOG_VERSION":   "v0.2.0",
	}
	want := []map[string]string{
		{
			"MESSAGE":        "threshold override active: gpu_count.expected_gpu_count = 8 (catalog default 4)",
			"CHECK_ID":       "gpu_count",
			"THRESHOLD_NAME": "expected_gpu_count",
			"ORIGINAL_VALUE": "4",
			"OVERRIDE_VALUE": "8",
			"PROVENANCE":     "nvidia",
		},
		{
			"MESSAGE":        "threshold override active: gpu_power_limit.expected_power_limit_w = 1000 (catalog default 1200)",
			"CHECK_ID":       "gpu_power_limit",
			"THRESHOLD_NAME": "expected_power_limit_w",
			"ORIGINAL_VALUE": "1200",
			"OVERRIDE_VALUE": "1000",
			"PROVENANCE":     "private",
		},
	}
	if len(rec.datagrams) != len(want) {
		t.Fatalf("wrote %d entries, want %d", len(rec.datagrams), len(want))
	}
	for i, w := range want {
		for k, v := range shared {
			w[k] = v
		}
		if got := parseDatagram(t, rec.datagrams[i]); !reflect.DeepEqual(got, w) {
			t.Errorf("entry %d = %v, want %v", i, got, w)
		}
	}
}

func TestWriteOverrideAuditStopsAtFirstFailure(t *testing.T) {
	records := []overrideRecord{
		{checkID: "gpu_count", thresholdName: "expected_gpu_count"},
		{checkID: "nvlink_active", thresholdName: "max_inactive_nvlinks"},
	}
	rec := datagramRecorder{writeErr: errors.New("socket closed"), failAt: 1}

	err := writeOverrideAudit(&rec, records, health.NodeVerdict{}, "node-1", "health-validation")
	if err == nil || !strings.Contains(err.Error(), "nvlink_active.max_inactive_nvlinks") {
		t.Fatalf("writeOverrideAudit() error = %v, want it to name nvlink_active.max_inactive_nvlinks", err)
	}
	if len(rec.datagrams) != 1 {
		t.Errorf("wrote %d entries before the failure, want 1", len(rec.datagrams))
	}
}

const twoOverrides = "checks:\n  gpu_count:\n    expected_gpu_count: 8\n  nvlink_active:\n    max_inactive_nvlinks: 2\n"

// auditApp returns an App on a GB200 fakeHost whose node backend reports 8
// GPUs, the flag recording whether GPU collection ran, and `run` args that
// load config as the config file when it is non-empty.
func auditApp(t *testing.T, config string) (*App, *bool, []string) {
	t.Helper()
	gpuCollected := new(bool)
	a := New().WithProgramName("test").WithNodeID(testNodeID)
	a.host = fakeHost{b: spyBackend{called: gpuCollected}}
	a.WithNodeBackend(toolkit.MockNode(node.NodeMeasurement{GPUCount: 8}))
	a.WithXidBackend(xidSpyBackend{called: new(bool)})
	a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
	a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
	if config == "" {
		return a, gpuCollected, nil
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, gpuCollected, []string{"--config", path}
}

func TestRunCommandAuditsActiveOverrides(t *testing.T) {
	a, _, args := auditApp(t, twoOverrides)
	var rec datagramRecorder
	a.openJournal = func() (io.WriteCloser, error) { return &rec, nil }

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCommand exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}

	want := []struct{ check, threshold, original, value string }{
		{"gpu_count", "expected_gpu_count", "4", "8"},
		{"nvlink_active", "max_inactive_nvlinks", "0", "2"},
	}
	if len(rec.datagrams) != len(want) {
		t.Fatalf("wrote %d audit entries, want %d", len(rec.datagrams), len(want))
	}
	for i, w := range want {
		got := parseDatagram(t, rec.datagrams[i])
		ts, err := time.Parse(time.RFC3339Nano, got["TIMESTAMP"])
		if got["CHECK_ID"] != w.check || got["THRESHOLD_NAME"] != w.threshold ||
			got["ORIGINAL_VALUE"] != w.original || got["OVERRIDE_VALUE"] != w.value ||
			got["TRACE_ID"] != verdict.TraceID || got["NODE_ID"] != testNodeID ||
			got["CATALOG_VERSION"] != verdict.CatalogVersion || err != nil || !ts.Equal(verdict.Timestamp) {
			t.Errorf("entry %d = %v, want %+v stamped with the verdict's trace ID, node ID, catalog version, and timestamp", i, got, w)
		}
	}
}

func TestRunCommandAuditsOverrideOnDisabledCheck(t *testing.T) {
	a, _, args := auditApp(t, "checks:\n  gpu_count:\n    expected_gpu_count: 8\ndisabled_checks:\n  - gpu_count\n")
	var rec datagramRecorder
	a.openJournal = func() (io.WriteCloser, error) { return &rec, nil }

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCommand exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}
	if len(rec.datagrams) != 1 || parseDatagram(t, rec.datagrams[0])["CHECK_ID"] != "gpu_count" {
		t.Errorf("audit entries = %q, want one for gpu_count", rec.datagrams)
	}
}

func TestRunCommandAuditFailureWritesNoVerdict(t *testing.T) {
	tests := []struct {
		openJournal   func() (io.WriteCloser, error)
		name          string
		wantStderr    string
		wantCollected bool
	}{
		{
			name:        "open fails before collection",
			openJournal: func() (io.WriteCloser, error) { return nil, errors.New("no journald") },
			wantStderr:  "audit threshold overrides: no journald",
		},
		{
			name: "write fails after collection",
			openJournal: func() (io.WriteCloser, error) {
				return &datagramRecorder{writeErr: errors.New("socket closed")}, nil
			},
			wantStderr:    "write audit entry for gpu_count.expected_gpu_count",
			wantCollected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, gpuCollected, args := auditApp(t, twoOverrides)
			a.openJournal = tt.openJournal

			var stdout, stderr bytes.Buffer
			if code := a.runCommand(args, &stdout, &stderr); code != exitError {
				t.Errorf("runCommand exit = %d, want %d", code, exitError)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty on exit 2", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if *gpuCollected != tt.wantCollected {
				t.Errorf("GPU collected = %v, want %v", *gpuCollected, tt.wantCollected)
			}
		})
	}
}

func TestRunCommandWithoutOverridesSkipsJournal(t *testing.T) {
	for _, config := range []string{"", "disabled_checks:\n  - gpu_power_state\n"} {
		a, _, args := auditApp(t, config)
		a.openJournal = func() (io.WriteCloser, error) {
			t.Errorf("journal opened for config %q with no overrides", config)
			return nil, errors.New("unexpected")
		}

		var stdout, stderr bytes.Buffer
		if code := a.runCommand(args, &stdout, &stderr); code == exitError {
			t.Errorf("config %q: runCommand exit = %d; stderr = %q", config, code, stderr.String())
		}
	}
}

// TestRunCommandDiscoveryFailureSkipsAudit pins that a run which cannot
// discover the architecture applies no overrides and so audits none.
func TestRunCommandDiscoveryFailureSkipsAudit(t *testing.T) {
	a, _, args := auditApp(t, twoOverrides)
	a.host = erroringHost{b: spyBackend{called: new(bool)}}
	a.openJournal = func() (io.WriteCloser, error) {
		t.Error("journal opened on a discovery failure")
		return nil, errors.New("unexpected")
	}

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code != exitUnhealthy {
		t.Errorf("runCommand exit = %d, want %d; stderr = %q", code, exitUnhealthy, stderr.String())
	}
}
