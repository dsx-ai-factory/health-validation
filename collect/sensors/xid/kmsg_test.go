// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	kmsgsignals "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
	"github.com/stretchr/testify/require"
)

func TestKmsgSharedRecordsAndFacts(t *testing.T) {
	at := time.Unix(1700000000, 0)
	reads := 0
	s, err := NewKmsg(func(context.Context) ([]kmsgsource.Message, error) {
		reads++
		return []kmsgsource.Message{
			{BootID: "boot", Sequence: 7, Timestamp: at, Priority: 3, Body: "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)"},
			{BootID: "boot", Sequence: 8, Timestamp: at, Priority: 6, Body: "ordinary kernel record"},
		}, nil
	})
	require.NoError(t, err)
	cycle, err := collect.NewCycle("test", at.Add(time.Hour))
	require.NoError(t, err)
	batch, err := s.Measure(t.Context(), collect.WithCycle(cycle))
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	facts := map[string]*observation.Observation{}
	raw := 0
	for _, o := range batch.Observations {
		require.Nil(t, o.GetCollectionError())
		require.Equal(t, "boot", o.Target.GetKernelRecord().BootId)
		require.Equal(t, at.UTC(), o.ObservedAt.AsTime())
		require.NotEqual(t, xidsignals.ScanWindow.ID(), o.SignalId)
		if o.SignalId == kmsgsignals.Message.ID() || o.SignalId == kmsgsignals.Priority.ID() {
			raw++
			continue
		}
		require.EqualValues(t, 7, o.Target.GetKernelRecord().Sequence)
		require.Equal(t, KmsgID, o.Source)
		facts[o.SignalId] = o
	}
	require.Equal(t, 4, raw)
	require.EqualValues(t, 149, facts[xidsignals.Code.ID()].GetValue().GetIntValue())
	require.Equal(t, "0018:01:00", facts[xidsignals.PCIAddress.ID()].GetValue().GetStringValue())
	require.True(t, facts[xidsignals.NVLink5Fatal.ID()].GetValue().GetBoolValue())
	require.Equal(t, "0x004505c6", facts[xidsignals.NVLink5IntrInfo.ID()].GetValue().GetStringValue())
}

func TestKmsgSelectionAndErrors(t *testing.T) {
	at := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name    string
		readErr error
		code    string
	}{
		{name: "overrun", readErr: syscall.EPIPE, code: kmsgsensor.SourceErrorOverrun},
		{name: "terminal after overrun", readErr: errors.Join(syscall.EPIPE, syscall.EIO)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			s, err := NewKmsg(func(context.Context) ([]kmsgsource.Message, error) {
				reads++
				return []kmsgsource.Message{{BootID: "boot", Sequence: 0, Timestamp: at, Body: "NVRM: Xid (PCI:0000:01:00): 79, GPU has fallen off the bus"}}, tc.readErr
			})
			require.NoError(t, err)
			cycle, err := collect.NewCycle("test", at)
			require.NoError(t, err)
			batch, err := s.Measure(t.Context(), collect.WithCycle(cycle), collect.WithSignalID())
			require.NoError(t, err)
			require.Empty(t, batch.Observations)
			require.Zero(t, reads)
			_, err = s.Measure(t.Context(), collect.WithCycle(cycle), collect.WithSignalID(xidsignals.ScanWindow.ID()))
			require.Error(t, err)
			require.Zero(t, reads)
			_, err = s.Measure(t.Context())
			require.Error(t, err)
			require.Zero(t, reads)
			for range 2 {
				batch, err = s.Measure(t.Context(), collect.WithCycle(cycle), collect.WithSignalID(xidsignals.Code.ID()))
				require.NoError(t, err)
				require.Len(t, batch.Observations, 2)
				require.Equal(t, tc.code, batch.Observations[0].GetCollectionError().SourceError)
				require.EqualValues(t, 79, batch.Observations[1].GetValue().GetIntValue())
			}
			require.Equal(t, 2, reads)
		})
	}
}

func TestKmsgMalformedXidKeepsRawRecord(t *testing.T) {
	at := time.Unix(1700000000, 0)
	s, err := NewKmsg(func(context.Context) ([]kmsgsource.Message, error) {
		return []kmsgsource.Message{{BootID: "boot", Sequence: 2, Timestamp: at, Body: "NVRM: Xid (PCI:0000:01:00): invalid"}}, nil
	})
	require.NoError(t, err)
	cycle, err := collect.NewCycle("test", at)
	require.NoError(t, err)
	batch, err := s.Measure(t.Context(), collect.WithCycle(cycle), collect.WithSignalID(kmsgsignals.Message.ID(), xidsignals.PCIAddress.ID()))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 2)
	require.NotNil(t, batch.Observations[0].GetValue())
	require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[1].GetCollectionError().Category)
	require.EqualValues(t, 2, batch.Observations[1].Target.GetKernelRecord().Sequence)
}
