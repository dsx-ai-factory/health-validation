// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"github.com/stretchr/testify/require"
)

const bootID = "0123456789abcdef0123456789abcdef"

var entryTime = time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC)

func entryLine(cursor string, at time.Time, message string) string {
	return fmt.Sprintf(`{"__CURSOR":%q,"__REALTIME_TIMESTAMP":"%d","_BOOT_ID":%q,"MESSAGE":%q}`+"\n", cursor, at.UnixMicro(), bootID, message)
}

func lines(stdout string) journal.Runner {
	return failing(stdout, "", nil)
}

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

func measure(t *testing.T, ctx context.Context, run journal.Runner, opts ...collect.OpOption) *observation.ObservationBatch {
	t.Helper()
	cycle, err := collect.NewCycle("cycle-1", time.Now())
	require.NoError(t, err)
	batch, err := New(journal.New(run)).Measure(ctx, append([]collect.OpOption{collect.WithCycle(cycle)}, opts...)...)
	require.NoError(t, err)
	require.Equal(t, "cycle-1", batch.GetCollectionId())
	require.NoError(t, observation.ValidateBatch(batch))
	for _, o := range batch.GetObservations() {
		require.Equal(t, ID, o.GetSource())
	}
	return batch
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
		detail := o.GetCollectionError().GetDetail()
		require.LessOrEqual(t, len(detail), 256)
		require.True(t, utf8.ValidString(detail))
	}
}

func TestMeasureEmitsEventsAndMarker(t *testing.T) {
	stdout := entryLine("c1", entryTime, "usb 1-1: new device") +
		entryLine("c2", entryTime, "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus.") +
		entryLine("c3", entryTime.Add(time.Second), "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus.") +
		entryLine("c4", entryTime, "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 0x00000001 0x00000002)") +
		entryLine("c5", entryTime, "NVRM: Xid (PCI:0000:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x1 (GPU Reset Required)")
	got := keyed(t, measure(t, context.Background(), lines(stdout)))

	for _, cursor := range []string{"c2", "c3"} {
		code := got[xidsignals.Code.ID()+"|"+cursor]
		require.NotNil(t, code, cursor)
		require.Equal(t, int64(79), code.GetValue().GetIntValue())
		require.Equal(t, bootID, code.GetTarget().GetJournalEntry().GetBootId())
		require.Equal(t, "0000:01:00", got[xidsignals.PCIAddress.ID()+"|"+cursor].GetValue().GetStringValue())
		require.Equal(t, int64(1234), got[xidsignals.PID.ID()+"|"+cursor].GetValue().GetIntValue())
		require.Equal(t, "python3", got[xidsignals.ProcessName.ID()+"|"+cursor].GetValue().GetStringValue())
	}
	require.Equal(t, entryTime, got[xidsignals.Code.ID()+"|c2"].GetObservedAt().AsTime())
	require.Equal(t, entryTime.Add(time.Second), got[xidsignals.Code.ID()+"|c3"].GetObservedAt().AsTime())

	require.Equal(t, "NETIR_LINK_EVT", got[xidsignals.NVLink5SubComponent.ID()+"|c4"].GetValue().GetStringValue())
	require.True(t, got[xidsignals.NVLink5Fatal.ID()+"|c4"].GetValue().GetBoolValue())
	require.Equal(t, int64(8), got[xidsignals.NVLink5Link.ID()+"|c4"].GetValue().GetIntValue())
	require.Equal(t, "0x004505c6", got[xidsignals.NVLink5IntrInfo.ID()+"|c4"].GetValue().GetStringValue())
	require.Equal(t, "0x00000000", got[xidsignals.NVLink5ErrorStatus.ID()+"|c4"].GetValue().GetStringValue())
	require.Equal(t, "0x00000001 0x00000002", got[xidsignals.NVLink5DebugData.ID()+"|c4"].GetValue().GetStringValue())
	require.NotContains(t, got, xidsignals.PID.ID()+"|c4")

	require.Equal(t, "0x0", got[xidsignals.RecoveryActionFromCode.ID()+"|c5"].GetValue().GetStringValue())
	require.Equal(t, "None", got[xidsignals.RecoveryActionFromName.ID()+"|c5"].GetValue().GetStringValue())
	require.Equal(t, "0x1", got[xidsignals.RecoveryActionToCode.ID()+"|c5"].GetValue().GetStringValue())
	require.Equal(t, "GPU Reset Required", got[xidsignals.RecoveryActionToName.ID()+"|c5"].GetValue().GetStringValue())

	marker := got[xidsignals.ScanWindow.ID()+"|"]
	require.NotNil(t, marker)
	require.Equal(t, xidsignals.WindowBoot, marker.GetValue().GetStringValue())
	require.NotNil(t, marker.GetTarget().GetNode())

	units := map[string]*string{}
	for _, definition := range xidsignals.All() {
		units[definition.ID()] = definition.Unit()
	}
	for key, o := range got {
		require.Equal(t, units[o.GetSignalId()], o.Unit, key)
	}
	require.Len(t, got, 2*4+6+2+4+2+1)
}

func TestMeasureCleanScan(t *testing.T) {
	batch := measure(t, context.Background(), lines(entryLine("c1", entryTime, "usb 1-1: new device")+entryLine("c2", entryTime, "eth0: link up")))
	require.Len(t, batch.GetObservations(), 1)
	require.Equal(t, xidsignals.ScanWindow.ID(), batch.GetObservations()[0].GetSignalId())
}

func TestMeasureParseError(t *testing.T) {
	stdout := entryLine("c1", entryTime, "NVRM: Xid (PCI:0000:01:00): 79") +
		entryLine("c2", entryTime, "NVRM: Xid (PCI:0000:01:00): x, pid=4242")
	got := keyed(t, measure(t, context.Background(), lines(stdout)))

	var bad []*observation.Observation
	for key, o := range got {
		if strings.HasSuffix(key, "|c2") {
			bad = append(bad, o)
		}
	}
	require.Len(t, bad, 1)
	failure := bad[0].GetCollectionError()
	require.Equal(t, xidsignals.Code.ID(), bad[0].GetSignalId())
	require.Equal(t, observation.CollectionErrorCategoryParseError, failure.GetCategory())
	require.Equal(t, xidsignals.ReasonInvalidCode, failure.GetSourceError())
	require.True(t, strings.HasPrefix(failure.GetDetail(), "entry 2:"), failure.GetDetail())
	require.NotContains(t, failure.GetDetail(), "4242")
	require.LessOrEqual(t, len(failure.GetDetail()), 256)
	require.Equal(t, xidsignals.Code.Unit(), bad[0].Unit)
	require.Contains(t, got, xidsignals.Code.ID()+"|c1")
	require.Contains(t, got, xidsignals.ScanWindow.ID()+"|")
}

func TestMeasureIgnoresProcessNamedLikeXid(t *testing.T) {
	batch := measure(t, context.Background(), lines(entryLine("c1", entryTime, "Out of memory: Killed process 4242 (NVRM: Xid x) total-vm:1024kB, anon-rss:512kB, file-rss:0kB")))
	require.Len(t, batch.GetObservations(), 1)
	require.Equal(t, xidsignals.ScanWindow.ID(), batch.GetObservations()[0].GetSignalId())
}

func TestMeasureMissingTimestamp(t *testing.T) {
	stdout := entryLine("c1", time.UnixMicro(0), "NVRM: Xid (PCI:0000:01:00): 79") +
		`{"__CURSOR":"c2","_BOOT_ID":"` + bootID + `","MESSAGE":"NVRM: Xid (PCI:0000:01:00): 79"}` + "\n"
	got := keyed(t, measure(t, context.Background(), lines(stdout)))
	require.Len(t, got, 3)
	for _, cursor := range []string{"c1", "c2"} {
		o := got[xidsignals.Code.ID()+"|"+cursor]
		require.NotNil(t, o, cursor)
		require.Equal(t, xidsignals.ReasonMissingTimestamp, o.GetCollectionError().GetSourceError())
		require.Positive(t, o.GetObservedAt().GetSeconds())
	}
}

func TestMeasureParseErrorWithoutCodeSelected(t *testing.T) {
	batch := measure(t, context.Background(), lines(entryLine("c1", entryTime, "NVRM: Xid (PCI:0000:01:00): x")),
		collect.WithSignalID(xidsignals.PCIAddress.ID(), xidsignals.ScanWindow.ID()))
	requireScanFailure(t, batch, 2, observation.CollectionErrorCategoryParseError)
}

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
			name:     "xid entry without boot ID",
			run:      lines(`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1","MESSAGE":"NVRM: Xid (PCI:0000:01:00): 79"}` + "\n"),
			detail:   "journal entry 1 has no boot ID or cursor",
			category: observation.CollectionErrorCategoryParseError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := measure(t, context.Background(), test.run)
			requireScanFailure(t, batch, len(xidsignals.All()), test.category)
			require.Contains(t, batch.GetObservations()[0].GetCollectionError().GetDetail(), test.detail)
		})
	}
}

func TestMeasureSkipsUnrelatedEntryWithoutIdentity(t *testing.T) {
	batch := measure(t, context.Background(), lines(`{"__REALTIME_TIMESTAMP":"1","MESSAGE":"eth0: link up"}`+"\n"))
	require.Len(t, batch.GetObservations(), 1)
	require.Equal(t, xidsignals.ScanWindow.ID(), batch.GetObservations()[0].GetSignalId())
}

func TestMeasureDetailIsBoundedUTF8(t *testing.T) {
	batch := measure(t, context.Background(), failing("", strings.Repeat("é", 150), exitError(t)))
	requireScanFailure(t, batch, len(xidsignals.All()), observation.CollectionErrorCategorySourceError)
}

func TestMeasureTimeout(t *testing.T) {
	exitErr := exitError(t)
	blocking := func(ctx context.Context, _ []string, _, _ io.Writer) error {
		<-ctx.Done()
		return exitErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	requireScanFailure(t, measure(t, ctx, blocking), len(xidsignals.All()), observation.CollectionErrorCategoryTimeout)

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelExpired()
	immediate := func(ctx context.Context, _ []string, _, _ io.Writer) error { return ctx.Err() }
	requireScanFailure(t, measure(t, expired, immediate), len(xidsignals.All()), observation.CollectionErrorCategoryTimeout)
}

func TestMeasureRejectsConcurrentRead(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	sensor := New(journal.New(func(_ context.Context, _ []string, out, _ io.Writer) error {
		close(started)
		<-release
		_, _ = io.WriteString(out, entryLine("c1", entryTime, "eth0: link up"))
		return nil
	}))
	done := make(chan error, 1)
	go func() {
		_, err := sensor.Measure(context.Background())
		done <- err
	}()
	<-started
	batch, err := sensor.Measure(context.Background())
	close(release)
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Len(t, batch.GetObservations(), len(xidsignals.All()))
	failure := batch.GetObservations()[0].GetCollectionError()
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, failure.GetCategory())
	require.Contains(t, failure.GetDetail(), "xid journal read already in progress")
}

func TestMeasureSelectionErrors(t *testing.T) {
	called := false
	sensor := New(journal.New(func(context.Context, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	}))
	_, err := sensor.Measure(context.Background(), collect.WithSignalID("xid.unknown"))
	require.Error(t, err)
	batch, err := sensor.Measure(context.Background(), collect.WithSignalID())
	require.NoError(t, err)
	require.Empty(t, batch.GetObservations())
	require.False(t, called)
}
