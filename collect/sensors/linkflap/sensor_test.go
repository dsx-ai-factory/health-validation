// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package linkflap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/stretchr/testify/require"
)

const bootID = "0123456789abcdef0123456789abcdef"

var (
	scanTime = time.Date(2026, 10, 1, 12, 0, 0, 500000000, time.UTC)
	// windowStart is the default lookback's start, truncated to the second.
	windowStart = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
)

const (
	downLine = "mlx5_core 0000:5a:00.0 enp90s0np0: Link down"
	upLine   = "mlx5_core 0000:5a:00.0 enp90s0np0: Link up"
)

func entryLine(cursor string, at time.Time, message string) string {
	return fmt.Sprintf(`{"__CURSOR":%q,"__REALTIME_TIMESTAMP":"%d","_BOOT_ID":%q,"MESSAGE":%q}`+"\n", cursor, at.UnixMicro(), bootID, message)
}

func lines(stdout string) journal.Runner { return failing(stdout, "", nil) }

func failing(stdout, stderr string, err error) journal.Runner {
	return func(_ context.Context, _ []string, out, errOut io.Writer) error {
		_, _ = io.WriteString(out, stdout)
		_, _ = io.WriteString(errOut, stderr)
		return err
	}
}

func exitError(t *testing.T) error {
	t.Helper()
	err := exec.Command("false").Run()
	require.Error(t, err)
	return err
}

func measureWith(t *testing.T, ctx context.Context, s collect.Sensor, opts ...collect.OpOption) *observation.ObservationBatch {
	t.Helper()
	cycle, err := collect.NewCycle("cycle-1", scanTime)
	require.NoError(t, err)
	batch, err := s.Measure(ctx, append([]collect.OpOption{collect.WithCycle(cycle)}, opts...)...)
	require.NoError(t, err)
	require.Equal(t, "cycle-1", batch.GetCollectionId())
	require.NoError(t, observation.ValidateBatch(batch))
	for _, o := range batch.GetObservations() {
		require.Equal(t, ID, o.GetSource())
		require.Equal(t, scanTime, o.GetObservedAt().AsTime())
	}
	return batch
}

func measure(t *testing.T, ctx context.Context, run journal.Runner, opts ...collect.OpOption) *observation.ObservationBatch {
	t.Helper()
	return measureWith(t, ctx, New(journal.New(run)), opts...)
}

// keyed indexes observations by signal and journal cursor ("" when the
// observation has no journal-entry target).
func keyed(t *testing.T, batch *observation.ObservationBatch) map[string]*observation.Observation {
	t.Helper()
	out := map[string]*observation.Observation{}
	for _, o := range batch.GetObservations() {
		key := o.GetSignalId() + "|" + o.GetTarget().GetJournalEntry().GetCursor()
		require.NotContains(t, out, key)
		out[key] = o
	}
	return out
}

func requireScanFailure(t *testing.T, batch *observation.ObservationBatch, selected int, category observation.CollectionErrorCategory) {
	t.Helper()
	require.Len(t, batch.GetObservations(), selected)
	for _, o := range batch.GetObservations() {
		require.Nil(t, o.GetTarget(), o.GetSignalId())
		require.Equal(t, category, o.GetCollectionError().GetCategory(), o.GetSignalId())
	}
}

func marker(t *testing.T, got map[string]*observation.Observation) time.Time {
	t.Helper()
	o := got[ibsignals.LinkFlapScanSince.ID()+"|"]
	require.NotNil(t, o)
	require.NotNil(t, o.GetTarget().GetNode())
	return time.UnixMicro(o.GetValue().GetIntValue()).UTC()
}

func TestMeasureEmitsTransitionsAndMarker(t *testing.T) {
	down := windowStart.Add(10*time.Minute + 123*time.Microsecond)
	stdout := entryLine("c1", down, downLine) +
		entryLine("c2", down, "usb 1-1: new device") +
		entryLine("c3", down.Add(2*time.Minute), upLine) +
		entryLine("c4", down.Add(3*time.Minute), downLine)
	got := keyed(t, measure(t, context.Background(), lines(stdout)))

	for cursor, want := range map[string]struct {
		at    time.Time
		state string
	}{"c1": {down, ibsignals.LinkStateDown}, "c3": {down.Add(2 * time.Minute), ibsignals.LinkStateUp}, "c4": {down.Add(3 * time.Minute), ibsignals.LinkStateDown}} {
		iface := got[ibsignals.LinkTransitionInterface.ID()+"|"+cursor]
		require.NotNil(t, iface, cursor)
		require.Equal(t, "enp90s0np0", iface.GetValue().GetStringValue())
		require.Equal(t, bootID, iface.GetTarget().GetJournalEntry().GetBootId())
		require.Equal(t, want.state, got[ibsignals.LinkTransitionState.ID()+"|"+cursor].GetValue().GetStringValue())
		// The raw journal timestamp survives to the microsecond.
		require.Equal(t, want.at.UnixMicro(), got[ibsignals.LinkTransitionRealtime.ID()+"|"+cursor].GetValue().GetIntValue())
	}
	require.Equal(t, windowStart, marker(t, got))

	units := map[string]*string{}
	for _, definition := range ibsignals.LinkFlapSignals() {
		units[definition.ID()] = definition.Unit()
	}
	for key, o := range got {
		require.Equal(t, units[o.GetSignalId()], o.Unit, key)
	}
	require.Len(t, got, 3*3+1)
}

// TestMeasureCleanScan keeps a window without transitions distinguishable
// from a failed read: the marker alone is a successful empty scan.
func TestMeasureCleanScan(t *testing.T) {
	batch := measure(t, context.Background(), lines(entryLine("c1", windowStart.Add(time.Minute), "eth0: link up")))
	require.Len(t, batch.GetObservations(), 1)
	require.Equal(t, ibsignals.LinkFlapScanSince.ID(), batch.GetObservations()[0].GetSignalId())
	require.Nil(t, batch.GetObservations()[0].GetCollectionError())
}

func TestMeasureWindow(t *testing.T) {
	stdout := entryLine("before", windowStart.Add(-time.Microsecond), downLine) +
		entryLine("start", windowStart, downLine) +
		// Stamped ahead of the scan by a clock later stepped back.
		entryLine("future", scanTime.Add(time.Hour), upLine)
	got := keyed(t, measure(t, context.Background(), lines(stdout)))
	require.NotContains(t, got, ibsignals.LinkTransitionInterface.ID()+"|before")
	require.Contains(t, got, ibsignals.LinkTransitionInterface.ID()+"|start")
	require.Contains(t, got, ibsignals.LinkTransitionInterface.ID()+"|future")

	short := New(journal.New(lines(stdout)), WithLookback(time.Minute))
	got = keyed(t, measureWith(t, context.Background(), short))
	require.Equal(t, scanTime.Add(-time.Minute).Truncate(time.Second), marker(t, got))
	require.NotContains(t, got, ibsignals.LinkTransitionInterface.ID()+"|start")
}

func TestMeasureBootOffset(t *testing.T) {
	sensorWith := func(up time.Duration, err error, opts ...Option) collect.Sensor {
		s := New(journal.New(lines(entryLine("c1", windowStart, downLine))), opts...).(*sensor)
		s.uptime = func() (time.Duration, error) { return up, err }
		return s
	}
	tests := []struct {
		since time.Time
		name  string
		up    time.Duration
	}{
		// Booted 10m ago with a 5m offset: the window starts 5m ago.
		{name: "offset inside lookback", up: 10 * time.Minute, since: scanTime.Add(-5 * time.Minute).Truncate(time.Second)},
		// Booted 3h ago: boot + offset is before the lookback, which wins.
		{name: "lookback inside settled time", up: 3 * time.Hour, since: windowStart},
		// Still settling: the window is empty.
		{name: "within offset", up: time.Minute, since: scanTime.Truncate(time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := keyed(t, measureWith(t, context.Background(), sensorWith(test.up, nil, WithBootOffset(5*time.Minute))))
			require.Equal(t, test.since, marker(t, got))
		})
	}

	t.Run("uptime unreadable", func(t *testing.T) {
		batch := measureWith(t, context.Background(), sensorWith(0, fs.ErrNotExist, WithBootOffset(time.Minute)))
		requireScanFailure(t, batch, len(ibsignals.LinkFlapSignals()), observation.CollectionErrorCategorySourceError)
		require.Contains(t, batch.GetObservations()[0].GetCollectionError().GetDetail(), "/proc/uptime")
	})
	t.Run("uptime unread without offset", func(t *testing.T) {
		got := keyed(t, measureWith(t, context.Background(), sensorWith(0, errors.New("unused"))))
		require.Equal(t, windowStart, marker(t, got))
	})
}

func TestReadUptime(t *testing.T) {
	file := func(data string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			require.Equal(t, "/proc/uptime", path)
			return []byte(data), nil
		}
	}
	up, err := readUptime(file("3600.25 7000.00\n"))
	require.NoError(t, err)
	require.Equal(t, time.Hour+250*time.Millisecond, up)
	for _, bad := range []string{"", "x 1", "NaN 1", "+Inf 1", "-5 1", "1e30 1"} {
		_, err = readUptime(file(bad))
		require.Error(t, err, "%q", bad)
	}
	_, err = readUptime(func(string) ([]byte, error) { return nil, fs.ErrPermission })
	require.ErrorIs(t, err, fs.ErrPermission)
}

func TestMeasureInvalidOptions(t *testing.T) {
	for name, opt := range map[string]Option{"lookback": WithLookback(-time.Second), "boot offset": WithBootOffset(-time.Second)} {
		t.Run(name, func(t *testing.T) {
			called := false
			s := New(journal.New(func(context.Context, []string, io.Writer, io.Writer) error {
				called = true
				return nil
			}), opt)
			batch := measureWith(t, context.Background(), s)
			requireScanFailure(t, batch, len(ibsignals.LinkFlapSignals()), observation.CollectionErrorCategoryNotConfigured)
			require.Contains(t, batch.GetObservations()[0].GetCollectionError().GetDetail(), name)
			require.False(t, called)
		})
	}
}

// TestMeasureParseError fails only the transition that matched but did not
// parse; the consumer fails the whole measurement on it.
func TestMeasureParseError(t *testing.T) {
	stdout := entryLine("c1", windowStart.Add(time.Minute), downLine) +
		entryLine("c2", windowStart.Add(time.Minute), "mlx5_core 0000:5a:00.0 enp90s0np0 extra: Link down") +
		// A malformed line outside the window is not read.
		entryLine("c3", windowStart.Add(-time.Minute), "mlx5_core 0000:5a:00.0 a b: Link up")
	got := keyed(t, measure(t, context.Background(), lines(stdout)))

	bad := got[ibsignals.LinkTransitionInterface.ID()+"|c2"]
	require.NotNil(t, bad)
	failure := bad.GetCollectionError()
	require.Equal(t, observation.CollectionErrorCategoryParseError, failure.GetCategory())
	require.Equal(t, ibsignals.ReasonMissingInterface, failure.GetSourceError())
	require.True(t, strings.HasPrefix(failure.GetDetail(), "entry 2:"), failure.GetDetail())
	require.NotContains(t, failure.GetDetail(), "enp90s0np0")
	require.NotContains(t, got, ibsignals.LinkTransitionState.ID()+"|c2")
	require.NotContains(t, got, ibsignals.LinkTransitionInterface.ID()+"|c3")
	require.Contains(t, got, ibsignals.LinkTransitionInterface.ID()+"|c1")
	require.Contains(t, got, ibsignals.LinkFlapScanSince.ID()+"|")
}

// TestMeasureMissingTimestamp fails a transition that cannot be placed in
// the window instead of dropping it.
func TestMeasureMissingTimestamp(t *testing.T) {
	stdout := `{"__CURSOR":"c1","_BOOT_ID":"` + bootID + `","MESSAGE":"` + downLine + `"}` + "\n"
	got := keyed(t, measure(t, context.Background(), lines(stdout)))
	failure := got[ibsignals.LinkTransitionInterface.ID()+"|c1"].GetCollectionError()
	require.Equal(t, observation.CollectionErrorCategoryParseError, failure.GetCategory())
	require.Equal(t, "missing_timestamp", failure.GetSourceError())
}

func TestMeasureParseErrorWithoutInterfaceSelected(t *testing.T) {
	batch := measure(t, context.Background(), lines(entryLine("c1", windowStart, "mlx5_core 0000:5a:00.0 a b: Link up")),
		collect.WithSignalID(ibsignals.LinkTransitionState.ID(), ibsignals.LinkFlapScanSince.ID()))
	requireScanFailure(t, batch, 2, observation.CollectionErrorCategoryParseError)
}

// TestMeasureWholeScanFailures never falls back to /dev/kmsg: whatever the
// journal failure, the scan fails without a marker.
func TestMeasureWholeScanFailures(t *testing.T) {
	exitErr := exitError(t)
	tests := []struct {
		run      journal.Runner
		name     string
		detail   string
		category observation.CollectionErrorCategory
	}{
		{name: "not found", run: failing("", "", &exec.Error{Name: "journalctl", Err: exec.ErrNotFound}), category: observation.CollectionErrorCategoryUnavailable},
		{name: "permission", run: failing("", "", fmt.Errorf("start: %w", fs.ErrPermission)), category: observation.CollectionErrorCategoryPermissionDenied},
		{name: "exit status", run: failing("", "Failed to open journal", exitErr), category: observation.CollectionErrorCategorySourceError},
		{name: "zero entries", run: lines(""), category: observation.CollectionErrorCategoryUnavailable},
		{name: "malformed json", run: lines("{not json\n"), category: observation.CollectionErrorCategoryParseError},
		{
			name:     "transition without boot ID",
			run:      lines(`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1","MESSAGE":"` + downLine + `"}` + "\n"),
			detail:   "journal entry 1 has no boot ID or cursor",
			category: observation.CollectionErrorCategoryParseError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := measure(t, context.Background(), test.run)
			requireScanFailure(t, batch, len(ibsignals.LinkFlapSignals()), test.category)
			require.Contains(t, batch.GetObservations()[0].GetCollectionError().GetDetail(), test.detail)
		})
	}
}

func TestMeasureReadsCurrentBootKernelJournal(t *testing.T) {
	var args []string
	measure(t, context.Background(), func(_ context.Context, a []string, out, _ io.Writer) error {
		args = a
		_, err := io.WriteString(out, entryLine("c1", windowStart, "eth0: link up"))
		return err
	})
	require.Contains(t, args, "--dmesg")
	require.Contains(t, args, "--boot")
	require.Contains(t, args, fmt.Sprintf("--since=@%d", windowStart.Unix()))
}

func TestMeasureTimeout(t *testing.T) {
	exitErr := exitError(t)
	blocking := func(ctx context.Context, _ []string, _, _ io.Writer) error {
		<-ctx.Done()
		return exitErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	requireScanFailure(t, measure(t, ctx, blocking), len(ibsignals.LinkFlapSignals()), observation.CollectionErrorCategoryTimeout)
}

func TestMeasureRejectsConcurrentRead(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := New(journal.New(func(_ context.Context, _ []string, out, _ io.Writer) error {
		close(started)
		<-release
		_, _ = io.WriteString(out, entryLine("c1", windowStart, "eth0: link up"))
		return nil
	}))
	done := make(chan error, 1)
	go func() {
		_, err := s.Measure(context.Background())
		done <- err
	}()
	<-started
	batch, err := s.Measure(context.Background())
	close(release)
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Len(t, batch.GetObservations(), len(ibsignals.LinkFlapSignals()))
	failure := batch.GetObservations()[0].GetCollectionError()
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, failure.GetCategory())
	require.Contains(t, failure.GetDetail(), "already in progress")
}

func TestMeasureSelectionErrors(t *testing.T) {
	called := false
	s := New(journal.New(func(context.Context, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	}))
	_, err := s.Measure(context.Background(), collect.WithSignalID("infiniband.unknown"))
	require.Error(t, err)
	batch, err := s.Measure(context.Background(), collect.WithSignalID())
	require.NoError(t, err)
	require.Empty(t, batch.GetObservations())
	require.False(t, called)
}
