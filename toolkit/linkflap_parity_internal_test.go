// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	ibsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/infiniband"
	linkflapsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/linkflap"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectadapters"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/collectbridge"
)

// flapParityNow is both paths' scan time. It is in the past so the bridge
// accepts observations stamped with it.
var flapParityNow = time.Date(2026, 10, 1, 12, 0, 0, 250000000, time.UTC)

type flapParityEntry struct {
	at      time.Time
	message string
}

// flapParityFixture feeds the same kernel entries to both paths. legacyErr
// fails every legacy journalctl call and collectErr is returned by the
// collect runner.
type flapParityFixture struct {
	legacyErr  error
	collectErr error
	name       string
	entries    []flapParityEntry
	overrides  []catalog.Override
}

type flapParityVerdict struct {
	action   health.RecommendedAction
	severity health.Severity
}

// flapAt places message ago before the scan.
func flapAt(ago time.Duration, message string) flapParityEntry {
	return flapParityEntry{at: flapParityNow.Add(-ago), message: message}
}

func flapLine(iface, state string) string {
	return "mlx5_core 0000:5a:00.0 " + iface + ": Link " + state
}

// legacyFlapRun stands in for journalctl under the legacy reader: short-full
// lines in local time, filtered by --since, and the readability probe.
func legacyFlapRun(t *testing.T, f flapParityFixture) runFunc {
	return func(_ context.Context, match func(string) bool, args ...string) (journalRunResult, error) {
		if f.legacyErr != nil {
			return journalRunResult{}, f.legacyErr
		}
		probe := slices.Contains(args, "-n")
		var since time.Time
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "--since="); ok {
				var err error
				if since, err = time.ParseInLocation(journalSinceLayout, value, time.Local); err != nil {
					t.Fatal(err)
				}
			}
		}
		var result journalRunResult
		for _, e := range f.entries {
			if e.at.Before(since) {
				continue
			}
			line := e.message
			if !probe {
				line = e.at.In(time.Local).Format("Mon 2006-01-02 15:04:05 MST") + " host kernel: " + e.message
			}
			result.outputSeen = true
			if match(line) {
				result.lines = append(result.lines, line)
			}
		}
		return result, nil
	}
}

func collectFlapRun(f flapParityFixture) journal.Runner {
	return func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		for i, e := range f.entries {
			_, _ = fmt.Fprintf(stdout, `{"__CURSOR":"s=1;i=%d","__REALTIME_TIMESTAMP":"%d","_BOOT_ID":"0123456789abcdef0123456789abcdef","MESSAGE":%q}`+"\n",
				i+1, e.at.UnixMicro(), e.message)
		}
		return f.collectErr
	}
}

func legacyFlapMeasurement(ctx context.Context, t *testing.T, f flapParityFixture) infiniband.LinkFlapMeasurement {
	t.Helper()
	backend := &rdmaLinkFlapBackend{
		run:      legacyFlapRun(t, f),
		now:      func() time.Time { return flapParityNow.In(time.Local) },
		uptime:   func() (time.Duration, error) { return 0, errors.New("unused") },
		lookback: defaultLinkFlapLookback,
	}
	measurements, err := NewRDMALinkFlapSensor(backend).Measure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return onlyLinkFlap(t, measurements)
}

func collectFlapMeasurement(ctx context.Context, t *testing.T, f flapParityFixture) infiniband.LinkFlapMeasurement {
	t.Helper()
	cycle, err := collect.NewCycle("parity", flapParityNow)
	if err != nil {
		t.Fatal(err)
	}
	return onlyLinkFlap(t, bridgedLinkFlap(ctx, t, cycle, collectFlapRun(f)))
}

func bridgedLinkFlap(ctx context.Context, t *testing.T, cycle *collect.Cycle, run journal.Runner) []health.Measurement {
	t.Helper()
	adapter := collectadapters.InfiniBand()
	signals := slices.Sorted(maps.Keys(adapter.Signals()))
	runner := collectbridge.NewRunner(cycle)
	if err := runner.Register(linkflapsensor.New(journal.New(run)), signals...); err != nil {
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

func onlyLinkFlap(t *testing.T, measurements []health.Measurement) infiniband.LinkFlapMeasurement {
	t.Helper()
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1: %v", len(measurements), measurements)
	}
	m, ok := measurements[0].(infiniband.LinkFlapMeasurement)
	if !ok {
		t.Fatalf("got %T, want infiniband.LinkFlapMeasurement", measurements[0])
	}
	return m
}

func failedFlapSignals(m infiniband.LinkFlapMeasurement) []string {
	var out []string
	for _, e := range m.Errors() {
		out = append(out, e.FailedSignals()...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func flapParityCatalog(t *testing.T, overrides ...catalog.Override) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest),
		catalog.WithOverrides(catalog.NewOverrideSet(overrides...)))
	if err := cat.Register(infiniband.RDMALinkFlapFatal()); err != nil {
		t.Fatal(err)
	}
	return cat
}

func flapVerdict(t *testing.T, verdict health.NodeVerdict) flapParityVerdict {
	t.Helper()
	if len(verdict.CheckResults) != 1 || verdict.CheckResults[0].CheckID != "rdma_linkflap_fatal" {
		t.Fatalf("results = %+v skipped = %+v, want rdma_linkflap_fatal evaluated", verdict.CheckResults, verdict.SkippedChecks)
	}
	return flapParityVerdict{action: verdict.CheckResults[0].RecommendedAction, severity: verdict.CheckResults[0].Severity}
}

func evaluateFlap(t *testing.T, m health.Measurement, overrides ...catalog.Override) flapParityVerdict {
	t.Helper()
	verdict, err := flapParityCatalog(t, overrides...).EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	return flapVerdict(t, verdict)
}

func requireFlapParity(ctx context.Context, t *testing.T, f flapParityFixture) flapParityVerdict {
	t.Helper()
	legacy, bridged := legacyFlapMeasurement(ctx, t, f), collectFlapMeasurement(ctx, t, f)
	if !reflect.DeepEqual(legacy.OutageDurations, bridged.OutageDurations) || !reflect.DeepEqual(legacy.FlapEvents, bridged.FlapEvents) {
		t.Errorf("measurements differ:\n legacy=%+v\n collect=%+v", legacy, bridged)
	}
	// A failed collect scan reports no window, so it labels the failure with
	// the boot, as the legacy sensor's own failures do.
	if len(legacy.Errors()) == 0 && legacy.Since != bridged.Since {
		t.Errorf("since differs: legacy=%q collect=%q", legacy.Since, bridged.Since)
	}
	if l, c := failedFlapSignals(legacy), failedFlapSignals(bridged); !slices.Equal(l, c) {
		t.Errorf("failed signals differ: legacy=%v collect=%v", l, c)
	}
	l, c := evaluateFlap(t, legacy, f.overrides...), evaluateFlap(t, bridged, f.overrides...)
	if !reflect.DeepEqual(l, c) {
		t.Errorf("verdicts differ:\n legacy=%+v\n collect=%+v", l, c)
	}
	return c
}

// TestLinkFlapCollectParity runs the legacy journalctl backend and the
// collect sensor, through the bridge and the InfiniBand adapter, over the
// same kernel entries. Outages, flap events, the window, failed signals,
// and the rdma_linkflap_fatal verdict and action must match. Remove with the
// legacy link-flap sensor.
func TestLinkFlapCollectParity(t *testing.T) {
	notFound := &exec.Error{Name: "journalctl", Err: exec.ErrNotFound}
	tests := []struct {
		f    flapParityFixture
		want health.Severity
	}{
		{f: flapParityFixture{name: "clean window", entries: []flapParityEntry{flapAt(time.Minute, "usb 1-1: new device")}}, want: health.Pass},
		{f: flapParityFixture{name: "one 114s outage", entries: []flapParityEntry{
			flapAt(10*time.Minute, flapLine("enp90s0np0", "down")), flapAt(10*time.Minute-114*time.Second, flapLine("enp90s0np0", "up")),
		}}, want: health.Critical},
		{f: flapParityFixture{name: "29s outage passes", entries: []flapParityEntry{
			flapAt(time.Minute, flapLine("enp90s0np0", "down")), flapAt(31*time.Second, flapLine("enp90s0np0", "up")),
		}}, want: health.Pass},
		{f: flapParityFixture{name: "30s outage is fatal", entries: []flapParityEntry{
			flapAt(time.Minute, flapLine("enp90s0np0", "down")), flapAt(30*time.Second, flapLine("enp90s0np0", "up")),
		}}, want: health.Critical},
		{f: flapParityFixture{name: "sub-second times", entries: []flapParityEntry{
			flapAt(time.Minute+300*time.Millisecond, flapLine("ib0", "down")), flapAt(29*time.Second+800*time.Millisecond, flapLine("ib0", "up")),
		}}, want: health.Critical},
		{f: flapParityFixture{name: "consecutive downs", entries: []flapParityEntry{
			flapAt(5*time.Minute, flapLine("ib0", "down")), flapAt(4*time.Minute, flapLine("ib0", "down")), flapAt(3*time.Minute, flapLine("ib0", "up")),
		}}, want: health.Critical},
		{f: flapParityFixture{name: "unrecovered down", entries: []flapParityEntry{flapAt(5*time.Minute, flapLine("ib0", "down"))}}, want: health.Pass},
		{f: flapParityFixture{name: "up only", entries: []flapParityEntry{flapAt(5*time.Minute, flapLine("ib0", "up"))}}, want: health.Pass},
		{f: flapParityFixture{name: "interfaces", entries: []flapParityEntry{
			flapAt(5*time.Minute, flapLine("ib1", "down")), flapAt(5*time.Minute, flapLine("ib0", "down")),
			flapAt(4*time.Minute, flapLine("ib0", "up")), flapAt(5*time.Minute-5*time.Second, flapLine("ib1", "up")),
		}}, want: health.Critical},
		{f: flapParityFixture{name: "outage before the window", entries: []flapParityEntry{
			flapAt(2*time.Hour, flapLine("ib0", "down")), flapAt(90*time.Minute, flapLine("ib0", "up")),
		}}, want: health.Pass},
		{f: flapParityFixture{name: "down before the window, up inside", entries: []flapParityEntry{
			flapAt(2*time.Hour, flapLine("ib0", "down")), flapAt(30*time.Minute, flapLine("ib0", "up")),
		}}, want: health.Pass},
		{f: flapParityFixture{name: "noise that is not a transition", entries: []flapParityEntry{
			flapAt(time.Minute, "mlx5_core 0000:5a:00.0 enp90s0np0: Link upgrade complete"), flapAt(time.Minute, "e1000e eno1: NIC Link is Down"),
		}}, want: health.Pass},
		{f: flapParityFixture{name: "threshold override", overrides: []catalog.Override{
			{CheckID: "rdma_linkflap_fatal", ThresholdName: "fatal_flap_threshold", Value: int64(2)},
		}, entries: []flapParityEntry{
			flapAt(5*time.Minute, flapLine("ib0", "down")), flapAt(4*time.Minute, flapLine("ib0", "up")),
		}}, want: health.Pass},
		{f: flapParityFixture{name: "journalctl missing", legacyErr: notFound, collectErr: notFound}, want: health.Critical},
		{f: flapParityFixture{name: "journalctl exits non-zero", legacyErr: exitOneWithStderr(t), collectErr: exitOneWithStderr(t)}, want: health.Critical},
		{f: flapParityFixture{name: "empty journal"}, want: health.Critical},
	}
	for _, tt := range tests {
		t.Run(tt.f.name, func(t *testing.T) {
			if got := requireFlapParity(context.Background(), t, tt.f); got.severity != tt.want {
				t.Fatalf("severity = %v, want %v", got.severity, tt.want)
			}
		})
	}
}

// TestLinkFlapCollectTimeoutParity stalls both paths' journal reads past
// their timeout. Both must fail rdma_linkflap_fatal with a retry-collection
// action. Remove with the legacy link-flap sensor.
func TestLinkFlapCollectTimeoutParity(t *testing.T) {
	const timeout = 10 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	legacy, err := CollectWithOptions(flapParityCatalog(t), CollectionOptions{Timeout: timeout},
		NewRDMALinkFlapSensor(blockingLinkFlapBackend(release)))
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
	bridged := evaluateFlap(t, onlyLinkFlap(t, bridgedLinkFlap(context.Background(), t, cycle, blocking)))

	l := flapVerdict(t, legacy)
	if !reflect.DeepEqual(l, bridged) {
		t.Errorf("verdicts differ: legacy=%+v collect=%+v", l, bridged)
	}
	if bridged.severity != health.Critical || !bridged.action.Equal(health.Drain(health.RemediationActionRetryCollection)) {
		t.Errorf("collect = %+v, want CRITICAL with retry collection", bridged)
	}
}

// blockingLinkFlapBackend stalls every Measure until release is closed.
type blockingLinkFlapBackend <-chan struct{}

func (b blockingLinkFlapBackend) Measure(context.Context) ([]health.Measurement, error) {
	<-b
	return nil, nil
}

// TestLinkFlapCollectDocumentedDivergences pins where the collect path
// deliberately differs from the legacy one. Remove with the legacy
// link-flap sensor.
func TestLinkFlapCollectDocumentedDivergences(t *testing.T) {
	ctx := context.Background()
	checkCollection := health.Drain(health.RemediationActionCheckCollection)

	// The legacy parser took any token before ": Link" as the interface, so a
	// mangled down went unpaired and the window read as clean; collect fails
	// the check closed.
	t.Run("unparseable transition", func(t *testing.T) {
		f := flapParityFixture{entries: []flapParityEntry{
			flapAt(5*time.Minute, flapLine("enp90s0np0/x", "down")), flapAt(4*time.Minute, flapLine("enp90s0np0", "up")),
		}}
		if v := evaluateFlap(t, legacyFlapMeasurement(ctx, t, f)); v.severity != health.Pass {
			t.Fatalf("legacy = %+v, want PASS", v)
		}
		if v := evaluateFlap(t, collectFlapMeasurement(ctx, t, f)); v.severity != health.Critical || !v.action.Equal(checkCollection) {
			t.Fatalf("collect = %+v, want CRITICAL check collection", v)
		}
	})
	// The legacy pattern matched mlx5_core anywhere in a line, so another
	// subsystem's line that mentioned the driver and a link state counted as a
	// transition. Collect requires the driver's prefix at the start of the
	// message.
	t.Run("transition text inside another subsystem's line", func(t *testing.T) {
		f := flapParityFixture{entries: []flapParityEntry{
			flapAt(5*time.Minute, "a[1]: mlx5_core 0000:5a:00.0 eth0: Link down"), flapAt(4*time.Minute, "a[1]: mlx5_core 0000:5a:00.0 eth0: Link up"),
		}}
		if v := evaluateFlap(t, legacyFlapMeasurement(ctx, t, f)); v.severity != health.Critical {
			t.Fatalf("legacy = %+v, want CRITICAL", v)
		}
		if v := evaluateFlap(t, collectFlapMeasurement(ctx, t, f)); v.severity != health.Pass {
			t.Fatalf("collect = %+v, want PASS", v)
		}
	})
}

// TestLinkFlapCoexistsWithSysfsPortSensor registers the sysfs port sensor
// beside the journal link-flap sensor on one runner: port state and
// counters are separate coverage, not a replacement for journal history.
func TestLinkFlapCoexistsWithSysfsPortSensor(t *testing.T) {
	cycle, err := collect.NewCycle("coexist", flapParityNow)
	if err != nil {
		t.Fatal(err)
	}
	runner := collectbridge.NewRunner(cycle)
	ports := ibsensor.New(ibsource.New(fstest.MapFS{}))
	if err = runner.Register(ports, ports.Signals().IDs()...); err != nil {
		t.Fatal(err)
	}
	flaps := linkflapsensor.New(journal.New(collectFlapRun(flapParityFixture{entries: []flapParityEntry{flapAt(time.Minute, "eth0: link up")}})))
	if err = runner.Register(flaps, flaps.Signals().IDs()...); err != nil {
		t.Fatalf("link-flap sensor overlaps the sysfs port sensor: %v", err)
	}
	runner.Collect()
	adapter := collectadapters.InfiniBand()
	signals := slices.Sorted(maps.Keys(adapter.Signals()))
	measurements, err := runner.Sensor(collectbridge.Binding{
		Name: adapter.Name(), Signals: signals, Build: adapter.Build, Errored: adapter.Errored,
	}).Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m := onlyLinkFlap(t, measurements); len(m.Errors()) != 0 {
		t.Fatalf("link-flap measurement beside the port sensor failed: %v", m.Errors())
	}
}
