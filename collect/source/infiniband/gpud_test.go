// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
// GPUd fixture and expected LoadDevices output from ea5ab612abad200c7793220f0e7955bb7c8f6302.
// Parser cases copied from its InfiniBand class/class_test.go (Apache-2.0).

package infiniband

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUdFixtureParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/gpud-sysfs.json")
	require.NoError(t, err)
	var files map[string]string
	require.NoError(t, json.Unmarshal(raw, &files))
	sysfs := fstest.MapFS{}
	for name, value := range files {
		sysfs[name] = &fstest.MapFile{Data: []byte(value)}
	}
	raw, err = os.ReadFile("testdata/gpud-devices.json")
	require.NoError(t, err)
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var devices []map[string]any
	require.NoError(t, decoder.Decode(&devices))
	require.Len(t, devices, 9)
	expected := make(map[string]map[string]any)
	for _, device := range devices {
		name := device["name"].(string)
		expected[name] = device
		for _, p := range device["ports"].([]any) {
			port := p.(map[string]any)
			expected[name+"/"+string(port["port"].(json.Number))] = port
		}
	}
	readings, err := New(sysfs).Read(context.Background(), ibsignals.All())
	require.NoError(t, err)
	seen := make(map[string]bool)
	for _, reading := range readings {
		id := reading.Target.GetInfiniBandDevice().GetName()
		if port := reading.Target.GetInfiniBandPort(); port != nil {
			id = port.GetDevice() + "/" + strconv.FormatUint(uint64(port.GetPort()), 10)
		}
		key := id + ":" + reading.Signal.ID()
		require.False(t, seen[key], "duplicate %s", key)
		seen[key] = true
		fields := expected[id]
		require.NotNil(t, fields, "unexpected target %s", id)
		file := paths[reading.Signal.ID()]
		var want any
		switch reading.Signal {
		case ibsignals.State:
			want = fields["state_id"]
		case ibsignals.PhysicalState:
			want = fields["phys_state_id"]
		case ibsignals.StateName:
			want = fields["state"]
		case ibsignals.PhysicalStateName:
			want = fields["phys_state"]
		case ibsignals.RateGbps:
			want = fields["rate_gb_sec"]
		default:
			if group := path.Dir(file); group != "." {
				want = fields[group].(map[string]any)[strings.ToLower(path.Base(file))]
			} else {
				want = fields[file]
			}
		}
		if want == nil {
			require.NotNil(t, reading.Failure, key)
			continue
		}
		require.Nil(t, reading.Failure, key)
		switch v := want.(type) {
		case string:
			require.Equal(t, v, reading.Value.GetStringValue(), key)
		case json.Number:
			if reading.Signal == ibsignals.RateGbps {
				n, parseErr := v.Float64()
				require.NoError(t, parseErr)
				require.Equal(t, n, reading.Value.GetDoubleValue(), key)
			} else {
				n, parseErr := v.Int64()
				require.NoError(t, parseErr)
				require.Equal(t, n, reading.Value.GetIntValue(), key)
			}
		default:
			require.FailNowf(t, "unexpected GPUd value", "%s: %T", key, want)
		}
	}
	require.Len(t, readings, 9*len(ibsignals.All()))
}

func TestGPUdDiscoveryAndCounterParsing(t *testing.T) {
	files := fstest.MapFS{
		"mlx5_0/ports/10/link_layer":    {Data: []byte("InfiniBand")},
		"mlx5_0/ports/10/state":         {Data: []byte("4: ACTIVE")},
		"mlx5_0/ports/2/link_layer":     {Data: []byte("InfiniBand")},
		"mlx5_0/ports/2/state":          {Data: []byte("1: DOWN")},
		"mlx5_0/ports/not-a-port/state": {},
		"renamed/ports/1/state":         {},
		"._mlx5_0/ports/1/state":        {},
		"MLX5_1/ports/1/state":          {},
	}
	readings, err := New(files).Read(context.Background(), collect.Signals{ibsignals.State, ibsignals.StateName})
	require.NoError(t, err)
	require.Len(t, readings, 4)
	require.Equal(t, "mlx5_0", readings[0].Target.GetInfiniBandPort().GetDevice())
	require.Equal(t, uint32(2), readings[0].Target.GetInfiniBandPort().GetPort())
	require.Equal(t, "DOWN", readings[1].Value.GetStringValue())
	require.Equal(t, "mlx5_0", readings[2].Target.GetInfiniBandPort().GetDevice())
	require.Equal(t, uint32(10), readings[2].Target.GetInfiniBandPort().GetPort())
	for _, raw := range []string{"0x10", "020", "0b10000"} {
		value, parseErr := parse(ibsignals.PortRcvData, raw)
		require.NoError(t, parseErr)
		require.Equal(t, int64(64), value.GetIntValue())
	}
	files["mlx5_0/ports/2/counters/link_downed"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("target")}
	readings, err = New(files).Read(context.Background(), collect.Signals{ibsignals.LinkDowned})
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryUnsupported, readings[0].Failure.Category)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, readings[1].Failure.Category)
	for _, err := range []error{fs.ErrInvalid, syscall.EINVAL, syscall.EOPNOTSUPP} {
		require.Equal(t, observation.CollectionErrorCategoryUnsupported, Failure(err).Category)
	}
}

func TestParseState(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedStr string
		expectedID  uint
		expectError bool
	}{
		{
			name:        "valid state",
			input:       "4: ACTIVE",
			expectedID:  4,
			expectedStr: "ACTIVE",
			expectError: false,
		},
		{
			name:        "valid state with spaces",
			input:       "  5  :  LinkUp  ",
			expectedID:  5,
			expectedStr: "LinkUp",
			expectError: false,
		},
		{
			name:        "invalid format - no colon",
			input:       "4 ACTIVE",
			expectError: true,
		},
		{
			name:        "invalid format - multiple colons",
			input:       "4:ACTIVE:extra",
			expectError: true,
		},
		{
			name:        "invalid ID",
			input:       "abc: ACTIVE",
			expectError: true,
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, str, err := parseState(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
				assert.Equal(t, tt.expectedStr, str)
			}
		})
	}
}

// TestParseRate tests the parseRate function with various inputs
func TestParseRate(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedRateGBSec float64
		expectedRate      uint64
		expectError       bool
	}{
		{
			name:              "valid rate - 100 Gb/sec",
			input:             "100 Gb/sec (4X EDR)",
			expectedRateGBSec: 100.0,
			expectedRate:      12500000000, // 100 * 125000000
			expectError:       false,
		},
		{
			name:              "valid rate - 400 Gb/sec",
			input:             "400 Gb/sec (4X NDR)",
			expectedRateGBSec: 400.0,
			expectedRate:      50000000000, // 400 * 125000000
			expectError:       false,
		},
		{
			name:              "valid rate with decimal",
			input:             "25.78125 Gb/sec",
			expectedRateGBSec: 25.78125,
			expectedRate:      3222656250, // 25.78125 * 125000000
			expectError:       false,
		},
		{
			name:        "invalid format - no space",
			input:       "100Gb/sec",
			expectError: true,
		},
		{
			name:        "invalid format - no unit",
			input:       "100",
			expectError: true,
		},
		{
			name:        "invalid number",
			input:       "abc Gb/sec",
			expectError: true,
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rateGBSec, rate, err := parseRate(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedRateGBSec, rateGBSec)
				assert.Equal(t, tt.expectedRate, rate)
			}
		})
	}
}

func TestGPUdOptionalValues(t *testing.T) {
	files := fstest.MapFS{
		"mlx5_0/ports/1/link_layer":                {Data: []byte("InfiniBand")},
		"mlx5_0/board_id":                          {},
		"mlx5_0/ports/1/counters/link_downed":      {Data: []byte("prefix N/A (no PMA)")},
		"mlx5_0/ports/1/hw_counters/out_of_buffer": {Data: []byte("N/A (not available)")},
	}
	readings, err := New(files).Read(context.Background(), collect.Signals{ibsignals.BoardID, ibsignals.LinkDowned, ibsignals.HardwareOutOfBuffer})
	require.NoError(t, err)
	require.Len(t, readings, 3)
	require.Nil(t, readings[0].Failure)
	require.Equal(t, "", readings[0].Value.GetStringValue())
	require.Equal(t, "mlx5_0", readings[0].Target.GetInfiniBandDevice().GetName())
	require.Nil(t, readings[1].Failure)
	require.Nil(t, readings[1].Value)
	require.Equal(t, observation.NoValueReasonNotSupported, readings[1].NoValue.GetReason())
	require.Equal(t, "prefix N/A (no PMA)", readings[1].NoValue.GetSourceStatus())
	require.Equal(t, "prefix N/A (no PMA)", readings[1].NoValue.GetDetail())
	for _, reading := range readings[1:] {
		require.Equal(t, "mlx5_0", reading.Target.GetInfiniBandPort().GetDevice())
		require.Equal(t, uint32(1), reading.Target.GetInfiniBandPort().GetPort())
	}
	require.Equal(t, observation.CollectionErrorCategoryParseError, readings[2].Failure.Category)
	// GPUd caches restricted counter files, not transient inventory failures.
	restricted := &restrictedFS{FS: files}
	source := New(restricted)
	for range 2 {
		_, readErr := source.Read(context.Background(), collect.Signals{ibsignals.BoardID})
		require.NoError(t, readErr)
	}
	require.Equal(t, 2, restricted.reads)
}
