// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

func TestRead(t *testing.T) {
	files := fstest.MapFS{
		"mlx5_0/ports/1/link_layer":                {Data: []byte("InfiniBand")},
		"mlx5_0/ports/1/state":                     {Data: []byte("4: ACTIVE\n")},
		"mlx5_0/ports/1/rate":                      {Data: []byte("2.5 Gb/sec (1X SDR)\n")},
		"mlx5_0/ports/1/counters/port_rcv_data":    {Data: []byte("123\n")},
		"mlx5_0/ports/1/counters/port_xmit_data":   {Data: []byte("18446744073709551615")},
		"mlx5_0/ports/1/hw_counters/out_of_buffer": {Data: []byte(" \tN/A (no PMA)\n")},
		"mlx5_1/ports/1/link_layer":                {Data: []byte("InfiniBand")},
		"mlx5_1/ports/1/state":                     {Data: []byte("bad state")},
	}
	s := New(files)
	signals := collect.Signals{ibsignals.State, ibsignals.Rate, ibsignals.PortRcvData, ibsignals.PortXmitData, ibsignals.HardwareOutOfBuffer}
	readings, err := s.Read(context.Background(), signals)
	require.NoError(t, err)
	require.Len(t, readings, 10)
	for i, reading := range readings {
		device := "mlx5_0"
		if i >= len(signals) {
			device = "mlx5_1"
		}
		require.Equal(t, device, reading.Target.GetInfiniBandPort().GetDevice().GetName())
		require.Equal(t, uint32(1), reading.Target.GetInfiniBandPort().GetPort())
	}
	for i, want := range []int64{4, 312500000, 492} {
		require.Equal(t, want, readings[i].Value.GetIntValue())
		require.Nil(t, readings[i].Failure)
	}
	require.Equal(t, observation.CollectionErrorCategoryParseError, readings[3].Failure.GetCategory())
	require.Nil(t, readings[4].Failure)
	require.Nil(t, readings[4].Value)
	require.Equal(t, observation.NoValueReasonNotSupported, readings[4].NoValue.GetReason())
	require.Equal(t, "N/A (no PMA)", readings[4].NoValue.GetSourceStatus())
	require.Equal(t, "N/A (no PMA)", readings[4].NoValue.GetDetail())
	require.Equal(t, observation.CollectionErrorCategoryParseError, readings[5].Failure.GetCategory())
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, readings[6].Failure.GetCategory())
	s = New(files, "mlx5_1")
	readings, err = s.Read(context.Background(), collect.Signals{ibsignals.State})
	require.NoError(t, err)
	require.Len(t, readings, 1)
	// Re-enumeration discovers newly added ports without rebuilding the source.
	files["mlx5_0/ports/2/link_layer"] = &fstest.MapFile{Data: []byte("InfiniBand")}
	files["mlx5_0/ports/2/state"] = &fstest.MapFile{Data: []byte("1: DOWN")}
	readings, err = s.Read(context.Background(), collect.Signals{ibsignals.State})
	require.NoError(t, err)
	require.Len(t, readings, 2)
}

type restrictedFS struct {
	fs.FS
	reads int
}

func (f *restrictedFS) ReadFile(name string) ([]byte, error) {
	if strings.HasSuffix(name, "/link_layer") {
		return fs.ReadFile(f.FS, name)
	}
	f.reads++
	return nil, syscall.EINVAL
}

func TestRestrictedFilesAndSelection(t *testing.T) {
	files := &restrictedFS{FS: fstest.MapFS{"mlx5_0/ports/1/link_layer": {Data: []byte("InfiniBand")}, "mlx5_0/ports/1/state": {}, "mlx5_0/ports/1/counters/link_downed": {}}}
	s := New(files)
	for range 2 {
		readings, err := s.Read(context.Background(), collect.Signals{ibsignals.LinkDowned})
		require.NoError(t, err)
		require.Len(t, readings, 1)
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, readings[0].Failure.GetCategory())
		require.Nil(t, readings[0].NoValue)
		require.Equal(t, "mlx5_0", readings[0].Target.GetInfiniBandPort().GetDevice().GetName())
		require.Equal(t, uint32(1), readings[0].Target.GetInfiniBandPort().GetPort())
	}
	require.Equal(t, 1, files.reads)
}

func TestParseBoundaries(t *testing.T) {
	for _, raw := range []string{"NaN Gb/sec", "+Inf Gb/sec", "-1 Gb/sec", "", "1e100 Gb/sec"} {
		_, err := parse(ibsignals.Rate, raw)
		assert.Error(t, err)
	}
	for _, raw := range []string{"-1", "18446744073709551615", "2305843009213693952", "invalid"} {
		_, err := parse(ibsignals.PortRcvData, raw)
		assert.Error(t, err)
	}
	_, err := parse(ibsignals.State, "4:ACTIVE:extra")
	assert.Error(t, err)
	require.Len(t, paths, len(ibsignals.All()))
	for _, signal := range ibsignals.All() {
		assert.NotEqual(t, "", paths[signal.ID()])
	}
}

// A host without the InfiniBand class has no devices, so Read reports
// nothing rather than a failure; a class that exists but is unreadable still
// fails.
func TestMissingClassIsNoDevices(t *testing.T) {
	s := New(os.DirFS(filepath.Join(t.TempDir(), "absent")))
	readings, err := s.Read(context.Background(), collect.Signals{ibsignals.State, ibsignals.FirmwareVersion})
	require.NoError(t, err)
	require.Empty(t, readings)
}

func TestDeviceInventoryWithoutPorts(t *testing.T) {
	s := New(fstest.MapFS{"mlx5_0/fw_ver": {Data: []byte("28.41.1000\n")}, "mlx5_0/board_id": {Data: []byte("board")}})
	readings, err := s.Read(context.Background(), collect.Signals{ibsignals.FirmwareVersion, ibsignals.BoardID})
	require.NoError(t, err)
	require.Len(t, readings, 2)
	require.Equal(t, "28.41.1000", readings[0].Value.GetStringValue())
	for _, reading := range readings {
		require.Equal(t, "mlx5_0", reading.Target.GetInfiniBandDevice().GetName())
	}
	readings, err = s.Read(context.Background(), collect.Signals{ibsignals.HCAType, ibsignals.State})
	require.NoError(t, err)
	require.Len(t, readings, 2)
	for _, reading := range readings {
		require.Equal(t, "mlx5_0", reading.Target.GetInfiniBandDevice().GetName())
		require.Equal(t, observation.CollectionErrorCategoryUnavailable, reading.Failure.GetCategory())
	}
}

// A consumer's Signal need not be comparable or use the library's concrete type.
type externalSignal []string

func (s externalSignal) ID() string    { return s[0] }
func (s externalSignal) Unit() *string { unit := "wrong"; return &unit }

func TestCanonicalSelection(t *testing.T) {
	s := New(fstest.MapFS{"mlx5_0/ports/1/link_layer": {Data: []byte("InfiniBand")}, "mlx5_0/ports/1/counters/port_rcv_data": {Data: []byte("10")}})
	definition := externalSignal{ibsignals.PortRcvData.ID()}
	readings, err := s.Read(context.Background(), collect.Signals{definition, definition})
	require.NoError(t, err)
	require.Len(t, readings, 1)
	require.Equal(t, int64(40), readings[0].Value.GetIntValue())
	require.Equal(t, "bytes", *readings[0].Signal.Unit())
}

func TestPortReadAlwaysIncludesLinkLayer(t *testing.T) {
	for _, layer := range []string{" InfiniBand\n", "Ethernet", "Unknown", ""} {
		t.Run(layer, func(t *testing.T) {
			s := New(fstest.MapFS{
				"mlx5_0/ports/1/link_layer": {Data: []byte(layer)},
				"mlx5_0/ports/1/state":      {Data: []byte("4: ACTIVE")},
			})
			readings, err := s.Read(context.Background(), collect.Signals{ibsignals.State})
			require.NoError(t, err)
			require.Len(t, readings, 1)
			require.Nil(t, readings[0].Failure)
			require.NotNil(t, readings[0].LinkLayer)
			require.Equal(t, strings.TrimSpace(layer), *readings[0].LinkLayer)
			require.Equal(t, int64(4), readings[0].Value.GetIntValue())
		})
	}
}
