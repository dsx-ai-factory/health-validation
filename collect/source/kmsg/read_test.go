// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadRecords(t *testing.T) {
	for _, end := range []error{syscall.EAGAIN, syscall.EIO} {
		calls := 0
		messages, err := readRecords(context.Background(), time.Unix(1000, 0), "boot", func(buf []byte) (int, error) {
			calls++
			switch calls {
			case 1:
				return 0, syscall.EINTR
			case 2:
				return copy(buf, "6,1,100,-;repeated body"), nil
			case 3, 4:
				return 0, syscall.EPIPE
			case 5:
				return 0, nil
			case 6:
				return copy(buf, "6,2,200,-;repeated body"), nil
			default:
				return 0, end
			}
		})
		require.Len(t, messages, 2)
		require.Equal(t, "boot", messages[0].BootID)
		require.Equal(t, int64(2), messages[1].Sequence)
		require.Equal(t, 7, calls)
		require.ErrorIs(t, err, syscall.EPIPE)
		if end == syscall.EIO {
			require.ErrorIs(t, err, syscall.EIO)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readRecords(ctx, time.Now(), "boot", func([]byte) (int, error) { require.FailNow(t, "read after cancellation"); return 0, nil })
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadRecordsTermination(t *testing.T) {
	for _, tt := range []struct {
		end          error
		name, record string
	}{
		{name: "empty ring", end: syscall.EAGAIN},
		{name: "malformed", record: "invalid", end: ErrMalformedRecord},
		{name: "read failure", end: syscall.EIO},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := readRecords(context.Background(), time.Unix(1000, 0), "boot", func(buf []byte) (int, error) {
				if tt.record != "" {
					return copy(buf, tt.record), nil
				}
				return 0, tt.end
			})
			require.Empty(t, messages)
			if tt.end == syscall.EAGAIN {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.end)
		})
	}
}

func TestBootTime(t *testing.T) {
	boot, err := parseBootTime("cpu 1 2 3\nintr 4\nbtime 1700000000\nprocesses 5\n")
	require.NoError(t, err)
	require.True(t, boot.Equal(time.Unix(1700000000, 0)))
	for _, input := range []string{"", "btime", "btime 1 2", "btime -1", "btime invalid"} {
		_, parseErr := parseBootTime(input)
		assert.Error(t, parseErr)
	}
}
