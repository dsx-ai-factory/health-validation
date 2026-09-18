// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
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
		if len(messages) != 2 || messages[0].BootID != "boot" || messages[1].Sequence != 2 || calls != 7 {
			t.Fatalf("records: %+v, calls: %d", messages, calls)
		}
		if !errors.Is(err, syscall.EPIPE) {
			t.Fatalf("lost records hidden: %v", err)
		}
		if end == syscall.EIO && !errors.Is(err, syscall.EIO) {
			t.Fatalf("read failure hidden: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readRecords(ctx, time.Now(), "boot", func([]byte) (int, error) { t.Fatal("read after cancellation"); return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
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
			if len(messages) != 0 {
				t.Fatalf("unexpected records: %v", messages)
			}
			if tt.end == syscall.EAGAIN {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, tt.end) {
				t.Fatalf("error: %v, want %v", err, tt.end)
			}
		})
	}
}

func TestBootTime(t *testing.T) {
	boot, err := parseBootTime("cpu 1 2 3\nintr 4\nbtime 1700000000\nprocesses 5\n")
	if err != nil || !boot.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("boot time: %v / %v", boot, err)
	}
	for _, input := range []string{"", "btime", "btime 1 2", "btime -1", "btime invalid"} {
		if _, parseErr := parseBootTime(input); parseErr == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
