// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// datagramRecorder stands in for the journald socket: each Write is one
// entry. With writeErr set, every Write fails.
type datagramRecorder struct {
	writeErr  error
	datagrams [][]byte
}

func (r *datagramRecorder) Write(b []byte) (int, error) {
	if r.writeErr != nil {
		return 0, r.writeErr
	}
	r.datagrams = append(r.datagrams, append([]byte(nil), b...))
	return len(b), nil
}

func (*datagramRecorder) Close() error { return nil }

// parseDatagram decodes a journal entry written in the NAME=value form.
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
			override: catalog.Override{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: float64(1000)},
			want:     overrideRecord{originalValue: float64(1200), value: float64(1000), checkID: "gpu_power_limit", thresholdName: "expected_power_limit_w", provenance: health.NVIDIA},
		},
		{
			arch:     "A100",
			override: catalog.Override{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: float64(1000)},
			want:     overrideRecord{value: float64(1000), checkID: "gpu_power_limit", thresholdName: "expected_power_limit_w", provenance: health.NVIDIA},
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
	var rec datagramRecorder
	records := []overrideRecord{{
		originalValue: float64(1200),
		value:         float64(1000),
		checkID:       "gpu_power_limit",
		thresholdName: "expected_power_limit_w",
		provenance:    health.Private,
	}}

	if err := writeOverrideAudit(&rec, records); err != nil {
		t.Fatalf("writeOverrideAudit() error = %v", err)
	}

	if len(rec.datagrams) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(rec.datagrams))
	}
	want := map[string]string{
		"MESSAGE_ID":     overrideAuditMessageID,
		"MESSAGE":        "threshold override active: gpu_power_limit.expected_power_limit_w = 1000 (catalog default 1200)",
		"PRIORITY":       "5",
		"CHECK_ID":       "gpu_power_limit",
		"THRESHOLD_NAME": "expected_power_limit_w",
		"ORIGINAL_VALUE": "1200",
		"OVERRIDE_VALUE": "1000",
		"PROVENANCE":     "private",
	}
	if got := parseDatagram(t, rec.datagrams[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("entry = %v, want %v", got, want)
	}
}

func TestWriteOverrideAuditRejectsNewline(t *testing.T) {
	var rec datagramRecorder
	records := []overrideRecord{{value: int64(1), checkID: "a\nb", thresholdName: "max_count"}}

	if err := writeOverrideAudit(&rec, records); err == nil {
		t.Fatal("writeOverrideAudit() error = nil, want an error for a newline in CHECK_ID")
	}
	if len(rec.datagrams) != 0 {
		t.Errorf("wrote %d entries, want none", len(rec.datagrams))
	}
}

const twoOverrides = "checks:\n  gpu_count:\n    expected_gpu_count: 8\n  nvlink_active:\n    max_inactive_nvlinks: 2\n"

// auditApp returns a hermetic GB200 App, the flag recording whether GPU
// collection ran, and `run` args that load config.
func auditApp(t *testing.T, config string) (*App, *bool, []string) {
	t.Helper()
	gpuCollected := new(bool)
	a := cadenceRunApp().WithNodeID(testNodeID)
	a.host = fakeHost{b: spyBackend{called: gpuCollected}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, gpuCollected, []string{"--config", path}
}

// An override on a disabled check is still active config, so it is recorded.
func TestRunCommandAuditsActiveOverrides(t *testing.T) {
	a, gpuCollected, args := auditApp(t, twoOverrides+"disabled_checks:\n  - gpu_count\n")
	var rec datagramRecorder
	a.openJournal = func() (io.WriteCloser, error) { return &rec, nil }

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
	if !*gpuCollected || stdout.Len() == 0 {
		t.Fatalf("run did not collect and write a verdict; stderr = %q", stderr.String())
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
		if got["CHECK_ID"] != w.check || got["THRESHOLD_NAME"] != w.threshold ||
			got["ORIGINAL_VALUE"] != w.original || got["OVERRIDE_VALUE"] != w.value {
			t.Errorf("entry %d = %v, want %+v", i, got, w)
		}
	}
}

// overrideRecords copies the engine's base-value rule, so the journal must
// report the same original value as the verdict for a check that runs.
func TestAuditOriginalValueMatchesVerdict(t *testing.T) {
	a, _, args := auditApp(t, "checks:\n  gpu_count:\n    expected_gpu_count: 8\n")
	var rec datagramRecorder
	a.openJournal = func() (io.WriteCloser, error) { return &rec, nil }

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code == exitError {
		t.Fatalf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	var original any
	for _, v := range verdict.CheckResults {
		if rt, ok := v.Thresholds["expected_gpu_count"]; ok && v.CheckID == "gpu_count" {
			original = rt.OriginalValue
			break
		}
	}
	if original == nil {
		t.Fatalf("no evaluated gpu_count result with expected_gpu_count in %s", stdout.String())
	}
	want, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.datagrams) != 1 {
		t.Fatalf("wrote %d audit entries, want 1", len(rec.datagrams))
	}
	if got := parseDatagram(t, rec.datagrams[0])["ORIGINAL_VALUE"]; got != string(want) {
		t.Errorf("journal ORIGINAL_VALUE = %s, verdict original_value = %s", got, want)
	}
}

func TestRunCommandAuditFailureWritesNoVerdict(t *testing.T) {
	tests := []struct {
		openJournal func() (io.WriteCloser, error)
		name        string
		wantStderr  string
	}{
		{
			name:        "open fails",
			openJournal: func() (io.WriteCloser, error) { return nil, errors.New("no journald") },
			wantStderr:  "test: audit threshold overrides: no journald\n",
		},
		{
			name: "write fails",
			openJournal: func() (io.WriteCloser, error) {
				return &datagramRecorder{writeErr: errors.New("socket closed")}, nil
			},
			wantStderr: "test: audit threshold overrides: write audit entry for gpu_count.expected_gpu_count: socket closed\n",
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
			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
			if *gpuCollected {
				t.Error("collected GPUs after the audit failed")
			}
		})
	}
}

func TestRunCommandWithoutOverridesSkipsJournal(t *testing.T) {
	a, _, args := auditApp(t, "disabled_checks:\n  - gpu_power_state\n")
	a.openJournal = func() (io.WriteCloser, error) {
		t.Error("journal opened with no overrides")
		return nil, errors.New("unexpected")
	}

	var stdout, stderr bytes.Buffer
	if code := a.runCommand(args, &stdout, &stderr); code == exitError {
		t.Errorf("runCommand exit = %d; stderr = %q", code, stderr.String())
	}
}

// A run that cannot discover the architecture applies no overrides, so it
// records none.
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

// collect applies no override to any evaluation, so it records none.
func TestCollectCommandSkipsJournal(t *testing.T) {
	a, _, args := auditApp(t, "checks:\n  gpu_count:\n    expected_gpu_count: 8\n")
	a.openJournal = func() (io.WriteCloser, error) {
		t.Error("journal opened by collect")
		return nil, errors.New("unexpected")
	}

	var stdout, stderr bytes.Buffer
	if code := a.collectCommand(args, &stdout, &stderr); code != exitOK {
		t.Errorf("collectCommand exit = %d, want %d; stderr = %q", code, exitOK, stderr.String())
	}
}
