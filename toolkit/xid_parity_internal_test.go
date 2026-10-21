// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os/exec"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	xidsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
)

type xidParityEntry struct {
	at      time.Time
	message string
}

// xidParityFixture feeds the same kernel entries to both paths. legacyErr is
// a failed legacy journal read and collectErr is returned by the collect
// runner.
type xidParityFixture struct {
	legacyErr  error
	collectErr error
	name       string
	entries    []xidParityEntry
}

type xidParityVerdict struct {
	action   health.RecommendedAction
	severity health.Severity
}

var xidParityAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func xidLines(messages ...string) []xidParityEntry {
	out := make([]xidParityEntry, 0, len(messages))
	for i, m := range messages {
		out = append(out, xidParityEntry{at: xidParityAt.Add(time.Duration(i) * time.Second), message: m})
	}
	return out
}

func collectXidRun(f xidParityFixture) journal.Runner {
	return func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		for i, e := range f.entries {
			_, _ = fmt.Fprintf(stdout, `{"__CURSOR":"s=1;i=%d","__REALTIME_TIMESTAMP":"%d","_BOOT_ID":"0123456789abcdef0123456789abcdef","MESSAGE":%q}`+"\n",
				i+1, e.at.UnixMicro(), e.message)
		}
		return f.collectErr
	}
}

func legacyXidMeasurement(ctx context.Context, t *testing.T, f xidParityFixture) xid.XidMeasurement {
	t.Helper()
	// MockXid runs the legacy parser over every kernel line, which the legacy
	// journal reader feeds it after its own filtering; a failed read becomes
	// the sensor's errored measurement, as the reader's does.
	lines := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		lines = append(lines, e.message)
	}
	sensor := NewXidSensor(MockXid(lines...))
	if f.legacyErr != nil {
		return onlyXid(t, sensor.Errored(f.legacyErr))
	}
	measurements, err := sensor.Measure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return onlyXid(t, measurements)
}

func collectXidMeasurement(ctx context.Context, t *testing.T, f xidParityFixture) xid.XidMeasurement {
	t.Helper()
	cycle, err := collect.NewCycle("parity", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return onlyXid(t, bridgedXid(ctx, t, cycle, collectXidRun(f)))
}

func bridgedXid(ctx context.Context, t *testing.T, cycle *collect.Cycle, run journal.Runner) []health.Measurement {
	t.Helper()
	adapter := collectadapters.XID()
	signals := slices.Sorted(maps.Keys(adapter.Signals()))
	runner := collectbridge.NewRunner(cycle)
	if err := runner.Register(xidsensor.New(journal.New(run)), signals...); err != nil {
		t.Fatal(err)
	}
	runner.Collect()
	measurements, err := runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: signals, Build: adapter.Build, Errored: adapter.Errored,
	}).Measure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return measurements
}

func onlyXid(t *testing.T, measurements []health.Measurement) xid.XidMeasurement {
	t.Helper()
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1: %v", len(measurements), measurements)
	}
	m, ok := measurements[0].(xid.XidMeasurement)
	if !ok {
		t.Fatalf("got %T, want xid.XidMeasurement", measurements[0])
	}
	return m
}

// xidStored lists the stored fields; comparing whole measurements would also
// compare their errors.
func xidStored(m xid.XidMeasurement) []any {
	return []any{m.Events, m.XidCounts, m.FatalXidCounts, m.EventCount, m.Since}
}

func failedXidSignals(m xid.XidMeasurement) []string {
	var out []string
	for _, e := range m.Errors() {
		out = append(out, e.FailedSignals()...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func xidParityCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	for _, check := range xid.Checks() {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

func evaluateXid(t *testing.T, m health.Measurement) health.NodeVerdict {
	t.Helper()
	verdict, err := xidParityCatalog(t).EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func xidParityVerdicts(t *testing.T, verdict health.NodeVerdict) map[string]xidParityVerdict {
	t.Helper()
	out := make(map[string]xidParityVerdict)
	for _, result := range verdict.CheckResults {
		out[result.CheckID] = xidParityVerdict{action: result.RecommendedAction, severity: result.Severity}
	}
	if len(out) != len(xid.Checks()) {
		t.Fatalf("evaluated %d checks %v, want %d", len(out), slices.Sorted(maps.Keys(out)), len(xid.Checks()))
	}
	return out
}

func requireXidParity(ctx context.Context, t *testing.T, f xidParityFixture) {
	t.Helper()
	legacy, bridged := legacyXidMeasurement(ctx, t, f), collectXidMeasurement(ctx, t, f)
	if l, c := xidStored(legacy), xidStored(bridged); !reflect.DeepEqual(l, c) {
		t.Errorf("measurements differ:\n legacy=%+v\n collect=%+v", legacy, bridged)
	}
	if l, c := failedXidSignals(legacy), failedXidSignals(bridged); !slices.Equal(l, c) {
		t.Errorf("failed signals differ: legacy=%v collect=%v", l, c)
	}
	if l, c := xidParityVerdicts(t, evaluateXid(t, legacy)), xidParityVerdicts(t, evaluateXid(t, bridged)); !reflect.DeepEqual(l, c) {
		t.Errorf("verdicts differ:\n legacy=%v\n collect=%v", l, c)
	}
}

const (
	xid79Line     = "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus."
	xid149Fatal   = "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)"
	xid145Line    = "NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02"
	xid154Line    = "NVRM: Xid (PCI:0000:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x1 (GPU Reset Required)"
	unrelatedLine = "usb 1-1: new high-speed USB device number 2"
)

// TestXidCollectParity runs the legacy journalctl backend and the collect
// sensor, through the bridge and Xid adapter, over the same kernel entries.
// Stored fields, failed signals, and verdicts must match. Remove with the
// legacy Xid sensor.
func TestXidCollectParity(t *testing.T) {
	notFound := &exec.Error{Name: "journalctl", Err: exec.ErrNotFound}
	tests := []xidParityFixture{
		{name: "clean boot", entries: xidLines(unrelatedLine, "eth0: link up")},
		{name: "Xid 79 with pid and name", entries: xidLines(unrelatedLine, xid79Line)},
		{name: "repeated Xid 79", entries: xidLines(xid79Line, xid79Line)},
		{name: "fatal 149 sub-code", entries: xidLines(xid149Fatal)},
		{name: "nonfatal 145 without tail", entries: xidLines(xid145Line)},
		{name: "Xid 154 transition", entries: xidLines(xid154Line)},
		{name: "interleaved", entries: xidLines(xid79Line, unrelatedLine, xid149Fatal, xid154Line, xid145Line, xid79Line)},
		{name: "unattributed NVLink5", entries: xidLines("NVRM: Xid (PCI:0018:01:00): 149, pid='<unknown>', name=<unknown>, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)")},
		{name: "unattributed ordinary", entries: xidLines("NVRM: Xid (PCI:0000:01:00): 79, pid='<unknown>', name=python3, x")},
		{name: "unterminated NVLink5 tail", entries: xidLines("NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000")},
		{name: "Xid stamped ahead of the clock", entries: []xidParityEntry{{at: time.Now().Add(time.Hour), message: xid79Line}}},
		{name: "Xid text without a device group", entries: xidLines(
			"Out of memory: Killed process 4242 (NVRM: Xid x) total-vm:1024kB, anon-rss:512kB, file-rss:0kB", "NVRM: Xid error: xyz, invalid data", "NVRM: Xid (invalid): some error")},
		{name: "unreadable journal", legacyErr: errors.New("journal unreadable, cannot confirm Xid state")},
		{name: "journalctl missing", legacyErr: notFound, collectErr: notFound},
		{name: "journalctl exits non-zero", legacyErr: exitOne(t), collectErr: exitOne(t)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { requireXidParity(context.Background(), t, tt) })
	}
}

// TestXidCollectOrdinaryAndNVLink5Equivalent covers the ordinary and NVLink5
// line forms on their own. Remove with the legacy Xid sensor.
func TestXidCollectOrdinaryAndNVLink5Equivalent(t *testing.T) {
	for name, line := range map[string]string{"ordinary": xid79Line, "NVLink5": xid149Fatal} {
		t.Run(name, func(t *testing.T) {
			requireXidParity(context.Background(), t, xidParityFixture{entries: xidLines(line)})
		})
	}
}

// TestXidCollectTimeoutParity stalls both paths' journal reads past their
// timeout. Both must fail every Xid check with a retry-collection action.
// Remove with the legacy Xid sensor.
func TestXidCollectTimeoutParity(t *testing.T) {
	const timeout = 10 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	legacy, err := CollectWithOptions(xidParityCatalog(t), CollectionOptions{Timeout: timeout},
		NewXidSensor(blockingXidBackend(release)))
	if err != nil {
		t.Fatal(err)
	}

	cycle, err := collect.NewCycleWithTimeout("parity", time.Now(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	blocking := func(ctx context.Context, _ []string, _, _ io.Writer) error {
		select {
		case <-ctx.Done():
		case <-release:
		}
		return ctx.Err()
	}
	bridged := evaluateXid(t, onlyXid(t, bridgedXid(context.Background(), t, cycle, blocking)))

	l, c := xidParityVerdicts(t, legacy), xidParityVerdicts(t, bridged)
	if !reflect.DeepEqual(l, c) {
		t.Errorf("verdicts differ: legacy=%v collect=%v", l, c)
	}
	for id, v := range c {
		if v.severity != health.Critical || !v.action.Equal(health.Drain(health.RemediationActionRetryCollection)) {
			t.Errorf("%s = %+v, want CRITICAL with retry collection", id, v)
		}
	}
}

// blockingXidBackend stalls every Measure until release is closed.
type blockingXidBackend <-chan struct{}

func (b blockingXidBackend) Measure(context.Context) ([]health.Measurement, error) {
	<-b
	return nil, nil
}

func requireXidAll(t *testing.T, verdict health.NodeVerdict, severity health.Severity, action *health.RecommendedAction) {
	t.Helper()
	for id, v := range xidParityVerdicts(t, verdict) {
		if v.severity != severity || (action != nil && !v.action.Equal(*action)) {
			t.Fatalf("%s = %+v, want %v %v", id, v, severity, action)
		}
	}
}

// TestXidCollectDocumentedDivergences pins where the collect path
// deliberately differs from the legacy one. Remove with the legacy Xid
// sensor.
func TestXidCollectDocumentedDivergences(t *testing.T) {
	ctx := context.Background()
	checkCollection := health.Drain(health.RemediationActionCheckCollection)
	failsClosed := func(t *testing.T, f xidParityFixture) {
		t.Helper()
		requireXidAll(t, evaluateXid(t, legacyXidMeasurement(ctx, t, f)), health.Pass, nil)
		requireXidAll(t, evaluateXid(t, collectXidMeasurement(ctx, t, f)), health.Critical, &checkCollection)
	}
	counts := func(t *testing.T, line string, code int64) {
		t.Helper()
		f := xidParityFixture{entries: xidLines(line)}
		if legacy := legacyXidMeasurement(ctx, t, f); legacy.EventCount != 0 {
			t.Fatalf("legacy counted %v", legacy.XidCounts)
		}
		if bridged := collectXidMeasurement(ctx, t, f); bridged.XidCounts[code] != 1 {
			t.Fatalf("collect counts = %v, want Xid %d", bridged.XidCounts, code)
		}
	}
	event := func(t *testing.T, line string) (xid.XidEvent, xid.XidEvent) {
		t.Helper()
		f := xidParityFixture{entries: xidLines(line)}
		legacy, bridged := legacyXidMeasurement(ctx, t, f), collectXidMeasurement(ctx, t, f)
		if len(legacy.Events) != 1 || len(bridged.Events) != 1 {
			t.Fatalf("events legacy=%v collect=%v", legacy.Events, bridged.Events)
		}
		return legacy.Events[0], bridged.Events[0]
	}

	t.Run("unparseable Xid line", func(t *testing.T) {
		failsClosed(t, xidParityFixture{entries: xidLines("NVRM: Xid (PCI:0000:01:00): x")})
	})
	t.Run("MIG attribution", func(t *testing.T) {
		counts(t, "NVRM: Xid (PCI:0000:01:00 GPU-I:01): 79, pid=1, name=a, x", 79)
	})
	t.Run("PCI token absent", func(t *testing.T) {
		counts(t, "NVRM: Xid (0000:03:00): 14, Channel 00000001", 14)
	})
	t.Run("numeric pid with unknown name", func(t *testing.T) {
		legacy, bridged := event(t, "NVRM: Xid (PCI:0000:01:00): 79, pid=42, name=<unknown>, x")
		if legacy.ProcessName != "<unknown>" || bridged.ProcessName != "" {
			t.Fatalf("process name legacy=%q collect=%q", legacy.ProcessName, bridged.ProcessName)
		}
	})
	t.Run("pid overflow", func(t *testing.T) {
		legacy, bridged := event(t, "NVRM: Xid (PCI:0000:01:00): 79, pid=999999999999999999999999999999, name=a, x")
		if legacy.Pid != math.MaxInt64 || bridged.Pid != 0 {
			t.Fatalf("pid legacy=%d collect=%d", legacy.Pid, bridged.Pid)
		}
	})
	t.Run("NVLink5 name padding", func(t *testing.T) {
		legacy, bridged := event(t, "NVRM: Xid (PCI:0000:5f:00): 145, pid=1, name=foo , RLW_REMAP Nonfatal XC0 i0 Link 02")
		if legacy.ProcessName != "foo " || bridged.ProcessName != "foo" {
			t.Fatalf("process name legacy=%q collect=%q", legacy.ProcessName, bridged.ProcessName)
		}
	})
	// The legacy reader's readability probe has no --dmesg, so it accepts a
	// boot whose journal holds no kernel entries as clean.
	t.Run("zero kernel entries", func(t *testing.T) {
		failsClosed(t, xidParityFixture{})
	})
}
