// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband reads port state and counters without persistence.
package infiniband

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

const (
	Name        = "infiniband.sysfs"
	DefaultRoot = "/sys/class/infiniband"
)

var ErrInUse = errors.New("InfiniBand read already in progress")

// Source owns a sysfs view and remembers restricted-PF files returning EINVAL.
// Recreate it after a driver/device reconfiguration to retry those files.
type Source struct {
	files      fs.FS
	excluded   map[string]bool
	restricted map[string]error
	mu         sync.Mutex
}

// New uses /sys/class/infiniband when files is nil. An fs.FS permits fixture
// testing and alternate host mounts without changing process-global paths.
func New(files fs.FS, excludedDevices ...string) *Source {
	if files == nil {
		files = os.DirFS(DefaultRoot)
	}
	excluded := make(map[string]bool, len(excludedDevices))
	for _, device := range excludedDevices {
		excluded[device] = true
	}
	return &Source{files: files, excluded: excluded, restricted: make(map[string]error)}
}

// Reading is one selected signal for a typed HCA or port target.
// A reported unsupported value is NoValue, not a collection failure.
type Reading struct {
	Signal  collect.Signal
	Value   *observation.Value
	NoValue *observation.NoValue
	Failure *observation.CollectionError
	Target  *observation.ObservationTarget
	// LinkLayer accompanies every port reading, even when not selected as a signal.
	// Nil means link_layer could not be read, or this is a device reading.
	LinkLayer *string
}

// Read re-enumerates ports on every call and preserves successful fields when
// a sibling field or device fails. Port reads always include link_layer, as GPUd
// does; all other files are read only when selected.
func (s *Source) Read(ctx context.Context, selected collect.Signals) ([]Reading, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if !s.mu.TryLock() {
		return nil, ErrInUse
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Resolve by ID before parsing so external Signal implementations cannot
	// change the canonical value type or unit, or duplicate a reading.
	selected, err := collect.NewOp(collect.WithSignalID(selected.IDs()...)).Signals(ibsignals.All())
	if err != nil {
		return nil, err
	}
	var deviceSignals, portSignals collect.Signals
	for _, signal := range selected {
		switch paths[signal.ID()] {
		case "fw_ver", "board_id", "hca_type":
			deviceSignals = append(deviceSignals, signal)
		default:
			portSignals = append(portSignals, signal)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	devices, err := fs.ReadDir(s.files, ".")
	if errors.Is(err, fs.ErrNotExist) {
		// No InfiniBand class means no HCAs, as on a host without RDMA
		// drivers; only a class that exists but cannot be read is a failure.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var readings []Reading
	cache := make(map[string]fileResult)
	for _, device := range devices {
		if s.excluded[device.Name()] || !strings.HasPrefix(device.Name(), "mlx") {
			continue
		}
		target := observation.InfiniBandDeviceTarget(device.Name())
		for _, signal := range deviceSignals {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			readings = append(readings, s.readSignal(signal, path.Join(device.Name(), paths[signal.ID()]), target, cache))
		}
		if len(portSignals) == 0 {
			continue
		}
		ports, portsErr := fs.ReadDir(s.files, path.Join(device.Name(), "ports"))
		if portsErr != nil {
			for _, signal := range portSignals {
				readings = append(readings, Reading{Signal: signal, Target: target, Failure: Failure(portsErr)})
			}
			continue
		}
		// GPUd orders ports numerically, not lexicographically.
		slices.SortStableFunc(ports, func(a, b fs.DirEntry) int {
			x, _ := strconv.ParseUint(a.Name(), 10, 32)
			y, _ := strconv.ParseUint(b.Name(), 10, 32)
			return cmp.Compare(x, y)
		})
		for _, port := range ports {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			portNumber, parseErr := strconv.ParseUint(port.Name(), 10, 32)
			if parseErr != nil {
				continue
			}
			portName := strconv.FormatUint(portNumber, 10)
			portPath := path.Join(device.Name(), "ports", portName)
			target := observation.InfiniBandPortTarget(device.Name(), uint32(portNumber))
			// GPUd's class collector requires link_layer before reading port values.
			layer := s.readSignal(ibsignals.LinkLayer, path.Join(portPath, "link_layer"), target, cache)
			if layer.Failure != nil {
				for _, signal := range portSignals {
					readings = append(readings, Reading{Signal: signal, Target: target, Failure: layer.Failure})
				}
				continue
			}
			linkLayer := layer.Value.GetStringValue()
			counterErrors := s.counterErrors(portPath, portSignals)
			for _, signal := range portSignals {
				if contextErr := ctx.Err(); contextErr != nil {
					return nil, contextErr
				}
				file := paths[signal.ID()]
				name := path.Join(portPath, file)
				if err := counterErrors[file]; err != nil {
					failure := Failure(err)
					failure.Operation = name
					readings = append(readings, Reading{Signal: signal, Target: target, Failure: failure, LinkLayer: &linkLayer})
					continue
				}
				reading := s.readSignal(signal, name, target, cache)
				reading.LinkLayer = &linkLayer
				readings = append(readings, reading)
			}
		}
	}
	return readings, nil
}

// counterErrors checks selected counters against directory entries before reading.
// GPUd skips nonregular files; each selected directory is listed only once.
func (s *Source) counterErrors(portPath string, signals collect.Signals) map[string]error {
	failures := make(map[string]error)
	for _, dir := range []string{"counters", "hw_counters"} {
		var selected []string
		for _, signal := range signals {
			if file := paths[signal.ID()]; path.Dir(file) == dir {
				selected = append(selected, file)
			}
		}
		if len(selected) == 0 {
			continue
		}
		entries, err := fs.ReadDir(s.files, path.Join(portPath, dir))
		files := make(map[string]fs.DirEntry, len(entries))
		for _, entry := range entries {
			files[entry.Name()] = entry
		}
		for _, file := range selected {
			switch entry := files[path.Base(file)]; {
			case err != nil:
				failures[file] = err
			case entry == nil:
				failures[file] = fs.ErrNotExist
			case !entry.Type().IsRegular():
				failures[file] = syscall.EOPNOTSUPP
			}
		}
	}
	return failures
}

func (s *Source) readFile(name string) ([]byte, error) {
	if err := s.restricted[name]; err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(s.files, name)
	// Repeated EINVAL reads on restricted PFs generate ACCESS_REG kernel errors.
	if group := path.Base(path.Dir(name)); errors.Is(err, syscall.EINVAL) && (group == "counters" || group == "hw_counters") {
		s.restricted[name] = err
	}
	return raw, err
}

// Failure normalizes source and collection-cycle errors without health policy.
func Failure(err error) *observation.CollectionError {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, fs.ErrInvalid), err.Error() == "operation not supported":
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, ErrInUse):
		category = observation.CollectionErrorCategoryUnavailable
	}
	return &observation.CollectionError{Category: category, Detail: err.Error()}
}

type fileResult struct {
	err error
	raw []byte
}

func (s *Source) readSignal(signal collect.Signal, name string, target *observation.ObservationTarget, cache map[string]fileResult) Reading {
	reading := Reading{Signal: signal, Target: target}
	result, cached := cache[name]
	if !cached {
		result.raw, result.err = s.readFile(name)
		cache[name] = result
	}
	raw, err := result.raw, result.err
	switch {
	case err != nil:
		reading.Failure = Failure(err)
	case path.Dir(paths[signal.ID()]) != "." && strings.Contains(string(raw), "N/A (no PMA)"):
		status := strings.TrimSpace(string(raw))
		reading.NoValue = &observation.NoValue{Reason: observation.NoValueReasonNotSupported, SourceStatus: status, Detail: status}
	default:
		reading.Value, err = parse(signal, string(raw))
		if err != nil {
			reading.Failure = &observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, Detail: err.Error()}
		}
	}
	if reading.Failure != nil {
		reading.Failure.Operation = name
	}
	return reading
}
