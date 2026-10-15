// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func testStat(pid uint32, name, state string, start uint64) string {
	return fmt.Sprintf("%d (%s) %s %s %d 0\n", pid, name, state, strings.Repeat("0 ", 18), start)
}

func TestParseStatIdentity(t *testing.T) {
	first, err := ParseStat([]byte(testStat(42, "name ) with (parens)", "D", 500)), 42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseStat([]byte(testStat(42, "new process", "R", 600)), 42)
	if err != nil {
		t.Fatal(err)
	}
	if first.StartTimeTicks == second.StartTimeTicks || first.State != "D" {
		t.Fatalf("identity/state lost: %+v %+v", first, second)
	}
	for _, bad := range []string{"42 broken", testStat(43, "wrong pid", "S", 500), testStat(42, "bad state", "?", 500)} {
		if _, err := ParseStat([]byte(bad), 42); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected malformed: %q: %v", bad, err)
		}
	}
}

func TestCommNoCommandLineFallback(t *testing.T) {
	reader := New(fstest.MapFS{"proc/42/comm": &fstest.MapFile{Data: []byte(" nvidia-blocker\n")}, "proc/42/cmdline": &fstest.MapFile{Data: []byte("secret full args")}})
	name, err := reader.Comm(42)
	if err != nil || name != " nvidia-blocker" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	if _, err := reader.Comm(43); err == nil {
		t.Fatal("missing comm must not fall back")
	}
}

func TestCmdlineBoundAndArguments(t *testing.T) {
	r := New(fstest.MapFS{"proc/42/cmdline": &fstest.MapFile{Data: []byte("python\x00worker.py\x00secret\x00")}})
	text, truncated, err := r.Cmdline(42, 4096)
	if err != nil || truncated || text != "python worker.py secret" {
		t.Fatalf("cmdline = %q %v %v", text, truncated, err)
	}
	text, truncated, err = r.Cmdline(42, 6)
	if err != nil || !truncated || text != "python" {
		t.Fatalf("bounded cmdline = %q %v %v", text, truncated, err)
	}
}

func TestProcessTextEncoding(t *testing.T) {
	r := New(fstest.MapFS{
		"proc/42/comm":    &fstest.MapFile{Data: []byte{'w', 0xff, '\n'}},
		"proc/42/cmdline": &fstest.MapFile{Data: []byte{'w', 0xff, 0}},
	})
	if _, err := r.Comm(42); !errors.Is(err, ErrMalformed) {
		t.Fatalf("non-UTF-8 comm: %v", err)
	}
	if _, _, err := r.Cmdline(42, 4096); !errors.Is(err, ErrMalformed) {
		t.Fatalf("non-UTF-8 cmdline: %v", err)
	}
	r = New(fstest.MapFS{"proc/42/cmdline": &fstest.MapFile{Data: []byte("a界\x00")}})
	for _, limit := range []int{2, 3} {
		text, truncated, err := r.Cmdline(42, limit)
		if err != nil || !truncated || text != "a" {
			t.Fatalf("limit %d: %q, %v, %v", limit, text, truncated, err)
		}
	}
	text, truncated, err := r.Cmdline(42, 4)
	if err != nil || !truncated || text != "a界" {
		t.Fatalf("complete character: %q, %v, %v", text, truncated, err)
	}
}
