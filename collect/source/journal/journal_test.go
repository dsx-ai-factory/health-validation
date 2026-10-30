// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package journal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

const bootID = "0123456789abcdef0123456789abcdef"

// fakeRunner writes canned output and returns err, recording the argv.
func fakeRunner(stdout, stderr string, err error, gotArgs *[]string) Runner {
	return func(_ context.Context, args []string, out, errOut io.Writer) error {
		if gotArgs != nil {
			*gotArgs = args
		}
		_, _ = io.WriteString(out, stdout)
		_, _ = io.WriteString(errOut, stderr)
		return err
	}
}

func entryJSON(cursor, realtime, message string) string {
	return fmt.Sprintf(`{"__CURSOR":%q,"__REALTIME_TIMESTAMP":%q,"_BOOT_ID":%q,"MESSAGE":%q}`, cursor, realtime, bootID, message)
}

func read(t *testing.T, ctx context.Context, r *Reader) ([]Entry, error) {
	t.Helper()
	var entries []Entry
	err := r.ReadKernelBoot(ctx, func(e Entry) error {
		entries = append(entries, e)
		return nil
	})
	return entries, err
}

func exitError(t *testing.T) *exec.ExitError {
	t.Helper()
	var exitErr *exec.ExitError
	require.ErrorAs(t, exec.Command("false").Run(), &exitErr)
	return exitErr
}

func TestReadKernelBootArgs(t *testing.T) {
	var args []string
	_, err := read(t, context.Background(), New(fakeRunner(entryJSON("c1", "1", "m")+"\n", "", nil, &args)))
	require.NoError(t, err)
	require.Equal(t, []string{"--dmesg", "--boot", "--no-pager", "--quiet", "--output=json", "--output-fields=MESSAGE", "--all"}, args)
}

func TestReadKernelBootEntries(t *testing.T) {
	stdout := strings.Join([]string{
		entryJSON("c1", "1700000000123456", "NVRM: Xid (PCI:0000:01:00): 79"),
		`{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1700000000123457","_BOOT_ID":"` + bootID + `","MESSAGE":[104,105,255]}`,
		`{"__CURSOR":"c3","__REALTIME_TIMESTAMP":"1700000000123458","_BOOT_ID":"` + bootID + `"}`,
		entryJSON("c4", "0", "zero time"),
		`{"__CURSOR":"c5","_BOOT_ID":"` + bootID + `","MESSAGE":"no time"}`,
	}, "\n")
	entries, err := read(t, context.Background(), New(fakeRunner(stdout, "", nil, nil)))
	require.NoError(t, err)
	require.Len(t, entries, 5)
	require.Equal(t, Entry{Realtime: time.UnixMicro(1700000000123456).UTC(), BootID: bootID, Cursor: "c1", Message: "NVRM: Xid (PCI:0000:01:00): 79"}, entries[0])
	require.Equal(t, "hi�", entries[1].Message)
	require.Empty(t, entries[2].Message)
	require.Equal(t, time.UnixMicro(1700000000123458).UTC(), entries[2].Realtime)
	require.True(t, entries[3].Realtime.IsZero())
	require.True(t, entries[4].Realtime.IsZero())
	require.Equal(t, "no time", entries[4].Message)
}

func TestReadKernelBootStopsOnCallbackError(t *testing.T) {
	stop := errors.New("stop")
	stdout := entryJSON("c1", "1", "a") + "\n" + entryJSON("c2", "2", "b") + "\n"
	calls := 0
	err := New(fakeRunner(stdout, "", nil, nil)).ReadKernelBoot(context.Background(), func(Entry) error {
		calls++
		return stop
	})
	require.ErrorIs(t, err, stop)
	require.Equal(t, 1, calls)
}

func TestReadKernelBootFailures(t *testing.T) {
	exitErr := exitError(t)
	longStderr := strings.Repeat("x", 10<<10)
	tests := []struct {
		run      Runner
		wantIs   error
		name     string
		wantText string
		expired  bool
	}{
		{name: "zero entries", run: fakeRunner("", "", nil, nil), wantIs: ErrNoEntries},
		{name: "malformed json", run: fakeRunner("{not json", "", nil, nil), wantIs: ErrMalformedEntry},
		{name: "realtime not a number", run: fakeRunner(entryJSON("c1", "abc", "m")+"\n", "", nil, nil), wantIs: ErrMalformedEntry},
		{name: "unterminated entry before another", run: fakeRunner(`{ "__CURSOR" : "c1", "__REALTIME_TIMESTAMP" : "1", "__MONOTONIC_TIMESTAMP" : "1", "_BOOT_ID" : "`+bootID+`"`+entryJSON("c2", "2", "NVRM: Xid (PCI:0000:01:00): 79")+"\n", "", nil, nil), wantIs: ErrMalformedEntry},
		{name: "oversized line", run: fakeRunner(strings.Repeat("a", maxLine+1)+"\n", "", nil, nil), wantIs: ErrMalformedEntry},
		{name: "exit status with stderr", run: fakeRunner("", "Failed to open journal\n", exitErr, nil), wantIs: exitErr, wantText: "Failed to open journal"},
		{name: "not found", run: fakeRunner("", "", &exec.Error{Name: "journalctl", Err: exec.ErrNotFound}, nil), wantIs: exec.ErrNotFound},
		{name: "permission", run: fakeRunner("", "", fmt.Errorf("start: %w", fs.ErrPermission), nil), wantIs: fs.ErrPermission},
		{name: "deadline wins over exit status", run: fakeRunner("", "", exitErr, nil), wantIs: context.DeadlineExceeded, expired: true},
		{name: "stderr is bounded", run: fakeRunner("", longStderr, exitErr, nil), wantIs: exitErr},
		{name: "stderr bound on a rune boundary", run: fakeRunner("", strings.Repeat("a", 255)+"é", exitErr, nil), wantIs: exitErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(1, 0))
				defer cancel()
			}
			_, err := read(t, ctx, New(test.run))
			require.ErrorIs(t, err, test.wantIs)
			require.Contains(t, err.Error(), test.wantText)
			if detail, ok := strings.CutPrefix(err.Error(), "journalctl: "+exitErr.Error()+": "); ok {
				require.LessOrEqual(t, len(detail), 256)
				require.True(t, utf8.ValidString(detail))
			}
		})
	}
}

func TestReadKernelBootIgnoresStderrOnSuccess(t *testing.T) {
	entries, err := read(t, context.Background(), New(fakeRunner(entryJSON("c1", "1", "m")+"\n", "Journal file is truncated, ignoring file.\n", nil, nil)))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return dir
}

func TestDefaultRunnerUsesPATH(t *testing.T) {
	dir := writeScript(t, `printf '%s\n' '`+entryJSON("c1", "1", "m")+`'`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	entries, err := read(t, context.Background(), New(nil))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestDefaultRunnerNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := read(t, context.Background(), New(nil))
	require.ErrorIs(t, err, exec.ErrNotFound)
}

func TestDefaultRunnerStopsOnDeadline(t *testing.T) {
	dir := writeScript(t, "exec /bin/sleep 30")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := read(t, ctx, New(nil))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestReadKernelSince(t *testing.T) {
	since := time.Date(2026, 10, 1, 11, 0, 0, 900000000, time.UTC)
	readSince := func(r *Reader) ([]Entry, error) {
		var entries []Entry
		err := r.ReadKernelSince(context.Background(), since, func(e Entry) error {
			entries = append(entries, e)
			return nil
		})
		return entries, err
	}
	// calls returns a runner answering the nth journalctl call with outputs[n].
	calls := func(argv *[][]string, outputs ...string) Runner {
		return func(_ context.Context, args []string, out, _ io.Writer) error {
			*argv = append(*argv, args)
			_, _ = io.WriteString(out, outputs[len(*argv)-1])
			return nil
		}
	}

	t.Run("window entries", func(t *testing.T) {
		var argv [][]string
		entries, err := readSince(New(calls(&argv, entryJSON("c1", "1", "m")+"\n")))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Len(t, argv, 1)
		require.Equal(t, append(slices.Clone(kernelBootArgs), fmt.Sprintf("--since=@%d", since.Unix())), argv[0])
	})
	t.Run("quiet window on a readable boot", func(t *testing.T) {
		var argv [][]string
		entries, err := readSince(New(calls(&argv, "", entryJSON("c9", "1", "older")+"\n")))
		require.NoError(t, err)
		require.Empty(t, entries)
		require.Len(t, argv, 2)
		require.Equal(t, append(slices.Clone(kernelBootArgs), "--lines=1"), argv[1])
	})
	t.Run("empty boot", func(t *testing.T) {
		var argv [][]string
		_, err := readSince(New(calls(&argv, "", "")))
		require.ErrorIs(t, err, ErrNoEntries)
	})
	t.Run("probe failure", func(t *testing.T) {
		n := 0
		_, err := readSince(New(func(_ context.Context, _ []string, _, errOut io.Writer) error {
			n++
			if n == 2 {
				_, _ = io.WriteString(errOut, "Failed to open journal")
				return exitError(t)
			}
			return nil
		}))
		require.ErrorContains(t, err, "Failed to open journal")
	})
}

func TestReadKernelBootRangeArgs(t *testing.T) {
	from, to := int64(100), int64(200)
	for _, tt := range []struct {
		from *int64
		to   *int64
		name string
		want []string
	}{
		{name: "unbounded"},
		{name: "start", from: &from, want: []string{"--since=@100"}},
		{name: "end", to: &to, want: []string{"--until=@200"}},
		{name: "both", from: &from, to: &to, want: []string{"--since=@100", "--until=@200"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var args []string
			calls := 0
			r := New(fakeRunner(entryJSON("c1", "150000000", "kernel event"), "", nil, &args))
			err := r.ReadKernelBootRange(context.Background(), tt.from, tt.to, func(e Entry) error {
				calls++
				require.Equal(t, "kernel event", e.Message)
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, append(append([]string(nil), kernelBootArgs...), tt.want...), args)
		})
	}
}

func TestReadKernelBootRangeEmptyWindow(t *testing.T) {
	from := int64(100)
	for _, mode := range []string{"empty", "empty exit 1", "read error", "stderr exit 1", "malformed", "probe empty", "probe error", "probe malformed", "probe timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			exitErr := exitError(t)
			calls := 0
			r := New(func(gotCtx context.Context, args []string, out, errOut io.Writer) error {
				require.Equal(t, ctx, gotCtx)
				calls++
				if calls == 1 {
					require.Equal(t, append(append([]string(nil), kernelBootArgs...), "--since=@100"), args)
					switch mode {
					case "empty exit 1":
						return exitErr
					case "read error":
						return fs.ErrPermission
					case "stderr exit 1":
						_, _ = io.WriteString(errOut, "unrecognized option")
						return exitErr
					case "malformed":
						_, _ = io.WriteString(out, "{broken json\n")
					}
					return nil
				}
				require.Equal(t, 2, calls)
				require.Equal(t, append(append([]string(nil), kernelBootArgs...), "-n", "1"), args)
				switch mode {
				case "probe empty":
					return nil
				case "probe error":
					return fs.ErrPermission
				case "probe malformed":
					_, _ = io.WriteString(out, "{broken json\n")
				case "probe timeout":
					cancel()
					return exitErr
				default:
					_, _ = io.WriteString(out, entryJSON("probe", "1", "boot event"))
				}
				return nil
			})
			err := r.ReadKernelBootRange(ctx, &from, nil, func(Entry) error {
				t.Fatal("probe entry must not reach caller")
				return nil
			})
			switch mode {
			case "empty", "empty exit 1":
				require.NoError(t, err)
			case "read error", "probe error":
				require.ErrorIs(t, err, fs.ErrPermission)
			case "stderr exit 1":
				require.ErrorIs(t, err, exitErr)
				require.ErrorContains(t, err, "unrecognized option")
			case "malformed", "probe malformed":
				require.ErrorIs(t, err, ErrMalformedEntry)
			case "probe empty":
				require.ErrorIs(t, err, ErrNoEntries)
			case "probe timeout":
				require.ErrorIs(t, err, context.Canceled)
			}
			if mode == "read error" || mode == "stderr exit 1" || mode == "malformed" {
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 2, calls)
			}
		})
	}
}
