// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package process reads process identity and current state from procfs.
package process

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

var ErrMalformed = errors.New("malformed procfs data")

// Stat identifies one process incarnation. State is the kernel state letter.
type Stat struct {
	State          string
	StartTimeTicks uint64
	PID            uint32
}

// Reader uses paths relative to a host filesystem root, not a procfs root.
// Names use comm; command-line reads require an explicit diagnostic call.
type Reader struct{ fsys fs.FS }

func New(fsys fs.FS) *Reader {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	return &Reader{fsys: fsys}
}

func (r *Reader) BootID() (string, error) {
	data, err := fs.ReadFile(r.fsys, "proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if len(id) != 36 {
		return "", fmt.Errorf("%w: invalid boot ID", ErrMalformed)
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", fmt.Errorf("%w: invalid boot ID", ErrMalformed)
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return "", fmt.Errorf("%w: invalid boot ID", ErrMalformed)
		}
	}
	return id, nil
}

func (r *Reader) PIDs() ([]uint32, error) {
	entries, err := fs.ReadDir(r.fsys, "proc")
	if err != nil {
		return nil, err
	}
	pids := make([]uint32, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.ParseUint(name, 10, 32)
		if err == nil && pid != 0 {
			pids = append(pids, uint32(pid))
		}
	}
	return pids, nil
}

func (r *Reader) Stat(pid uint32) (Stat, error) {
	data, err := fs.ReadFile(r.fsys, "proc/"+strconv.FormatUint(uint64(pid), 10)+"/stat")
	if err != nil {
		return Stat{}, err
	}
	return ParseStat(data, pid)
}

// ParseStat locates the last closing parenthesis because comm can contain spaces
// and parentheses. Field 22 is starttime, in clock ticks since boot.
func ParseStat(data []byte, pid uint32) (Stat, error) {
	text := string(data)
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	bad := func() (Stat, error) { return Stat{}, fmt.Errorf("%w: PID %d stat", ErrMalformed, pid) }
	if open < 1 || close <= open {
		return bad()
	}
	parsedPID, err := strconv.ParseUint(strings.TrimSpace(text[:open]), 10, 32)
	if err != nil || parsedPID != uint64(pid) {
		return bad()
	}
	fields := strings.Fields(text[close+1:])
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.Contains("RSDZTWtXxKPI", fields[0]) {
		return bad()
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return bad()
	}
	return Stat{PID: pid, StartTimeTicks: start, State: fields[0]}, nil
}

// Comm removes only the procfs trailing newline. A 15-byte name stays truncated;
// it never triggers a cmdline fallback. Embedded whitespace is part of the name.
func (r *Reader) Comm(pid uint32) (string, error) {
	data, err := fs.ReadFile(r.fsys, "proc/"+strconv.FormatUint(uint64(pid), 10)+"/comm")
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%w: empty comm for PID %d", ErrMalformed, pid)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

// Cmdline reads at most limit bytes plus one truncation probe. The caller must
// opt in and gate this read: a procfs cmdline read can wait on mmap_lock even
// after its context deadline. No environment variables are read.
func (r *Reader) Cmdline(pid uint32, limit int) (string, bool, error) {
	if limit < 1 || limit > 4096 {
		return "", false, fmt.Errorf("cmdline limit must be between 1 and 4096 bytes")
	}
	file, err := r.fsys.Open("proc/" + strconv.FormatUint(uint64(pid), 10) + "/cmdline")
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	text := strings.ReplaceAll(strings.TrimSuffix(string(data), "\x00"), "\x00", " ")
	return text, truncated, nil
}
