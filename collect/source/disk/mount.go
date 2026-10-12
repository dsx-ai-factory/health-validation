// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
)

// Mount retains kernel mount identity, including bind mounts and mount stacks.
type Mount struct {
	Namespace      string
	Root           string
	Point          string
	Options        string
	OptionalFields string
	FSType         string
	Source         string
	SuperOptions   string
	ID             uint64
	ParentID       uint64
	Major          uint32
	Minor          uint32
}

type MountFailure struct {
	Err error
	ID  uint64
}

// ParseMountInfo keeps valid records when another line is malformed.
func ParseMountInfo(reader io.Reader, namespace string) ([]Mount, []MountFailure) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var mounts []Mount
	var failures []MountFailure
	seen := make(map[uint64]bool)
	duplicates := make(map[uint64]bool)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		var id uint64
		if len(fields) > 0 {
			parsedID, parseErr := decimalIdentity(fields[0], 64)
			if parseErr == nil {
				id = parsedID
			}
		}
		if id != 0 {
			if seen[id] {
				if !duplicates[id] {
					failures = append(failures, MountFailure{ID: id, Err: fmt.Errorf("mountinfo: duplicate mount ID %d", id)})
				}
				duplicates[id] = true
				continue
			}
			seen[id] = true
		}
		mount, err := parseMount(fields, namespace)
		if err != nil {
			failures = append(failures, MountFailure{ID: id, Err: fmt.Errorf("mountinfo: %w", err)})
			continue
		}
		mounts = append(mounts, mount)
	}
	if err := scanner.Err(); err != nil {
		failures = append(failures, MountFailure{Err: err})
	}
	if len(duplicates) > 0 {
		valid := mounts[:0]
		for _, mount := range mounts {
			if !duplicates[mount.ID] {
				valid = append(valid, mount)
			}
		}
		mounts = valid
	}
	return mounts, failures
}

func parseMount(fields []string, namespace string) (Mount, error) {
	m := Mount{Namespace: namespace}
	separator := -1
	for i, field := range fields {
		if field == "-" {
			separator = i
			break
		}
	}
	if separator < 6 || len(fields) != separator+4 {
		return m, fmt.Errorf("invalid field count or separator")
	}
	var err error
	m.ID, err = decimalIdentity(fields[0], 64)
	if err != nil || m.ID == 0 {
		return m, fmt.Errorf("invalid mount ID %q", fields[0])
	}
	m.ParentID, err = decimalIdentity(fields[1], 64)
	if err != nil {
		return m, fmt.Errorf("invalid parent ID: %w", err)
	}
	m.Major, m.Minor, err = ParseDevice(fields[2])
	if err != nil {
		return m, err
	}
	values := []*string{&m.Root, &m.Point, &m.Options, &m.FSType, &m.Source, &m.SuperOptions}
	raw := []string{fields[3], fields[4], fields[5], fields[separator+1], fields[separator+2], fields[separator+3]}
	for i := range values {
		*values[i], err = unescapeMount(raw[i])
		if err != nil {
			return m, err
		}
	}
	if !strings.HasPrefix(m.Point, "/") || !strings.HasPrefix(m.Root, "/") {
		return m, fmt.Errorf("mount paths must be absolute")
	}
	m.OptionalFields = strings.Join(fields[6:separator], " ")
	return m, nil
}

func unescapeMount(raw string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			result.WriteByte(raw[i])
			continue
		}
		if i+3 >= len(raw) {
			return "", fmt.Errorf("truncated mount escape %q", raw)
		}
		escape := raw[i+1 : i+4]
		switch escape {
		case "040":
			result.WriteByte(' ')
		case "011":
			result.WriteByte('\t')
		case "012":
			result.WriteByte('\n')
		case "134":
			result.WriteByte('\\')
		default:
			return "", fmt.Errorf("invalid mount escape %q", escape)
		}
		i += 3
	}
	return result.String(), nil
}

func ParseDevice(raw string) (uint32, uint32, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid major:minor %q", raw)
	}
	major, err := decimalIdentity(parts[0], 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid major: %w", err)
	}
	minor, err := decimalIdentity(parts[1], 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minor: %w", err)
	}
	return uint32(major), uint32(minor), nil
}

func decimalIdentity(raw string, bits int) (uint64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty decimal identity")
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("invalid decimal identity %q", raw)
		}
	}
	return strconv.ParseUint(raw, 10, bits)
}

// ReadMounts uses one parser for disk inventory and FUSE correlation.
// The default namespace combines the boot ID and mount-namespace inode.
// A configured namespace must include the caller's required identity scope.
// mountPID selects proc/<PID> in the supplied procfs; zero selects proc/self.
// The caller must ensure that this process uses the intended mount namespace.
func ReadMounts(files fs.FS, namespace string, mountPID ...uint32) ([]Mount, []MountFailure, error) {
	pid := uint32(0)
	if len(mountPID) > 0 {
		pid = mountPID[0]
	}
	infoPath := MountInfoPath(pid)
	procPath := strings.TrimSuffix(infoPath, "/mountinfo")
	if namespace == "" {
		var err error
		namespace, err = fs.ReadLink(files, procPath+"/ns/mnt")
		if err != nil {
			return nil, nil, fmt.Errorf("read mount namespace: %w", err)
		}
		if !strings.HasPrefix(namespace, "mnt:[") || !strings.HasSuffix(namespace, "]") {
			return nil, nil, parseError(fmt.Errorf("invalid mount namespace %q", namespace))
		}
		inode, err := strconv.ParseUint(namespace[len("mnt:["):len(namespace)-1], 10, 64)
		if err != nil || inode == 0 {
			return nil, nil, parseError(fmt.Errorf("invalid mount namespace %q", namespace))
		}
		boot, err := fs.ReadFile(files, "proc/sys/kernel/random/boot_id")
		if err != nil {
			return nil, nil, fmt.Errorf("read mount boot identity: %w", err)
		}
		bootID := strings.TrimSpace(string(boot))
		if bootID == "" {
			return nil, nil, parseError(fmt.Errorf("empty mount boot identity"))
		}
		namespace = bootID + ":" + namespace
	}
	file, err := files.Open(infoPath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	mounts, failures := ParseMountInfo(file, namespace)
	return mounts, failures, nil
}

// MountInfoPath names the selected process view for acquisition and provenance.
func MountInfoPath(pid uint32) string {
	if pid == 0 {
		return "proc/self/mountinfo"
	}
	return "proc/" + strconv.FormatUint(uint64(pid), 10) + "/mountinfo"
}
