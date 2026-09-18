// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband reads port state and counters without persistence.
package infiniband

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
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

// Reading is one selected signal for an HCA or port.
type Reading struct {
	Signal  collect.Signal
	Value   *observation.Value
	Failure *observation.CollectionError
	Entity  *observation.Entity
}

// Read re-enumerates ports on every call and preserves successful fields when
// a sibling field or device fails. Only selected files are read.
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
		file, ok := paths[signal.ID()]
		if !ok {
			return nil, fmt.Errorf("unsupported InfiniBand signal %q", signal.ID())
		}
		switch file {
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
	if err != nil {
		return nil, err
	}
	var readings []Reading
	for _, device := range devices {
		if s.excluded[device.Name()] || strings.HasPrefix(device.Name(), ".") {
			continue
		}
		entity := &observation.Entity{Type: "infiniband_device", Id: device.Name()}
		for _, signal := range deviceSignals {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			readings = append(readings, s.readSignal(signal, path.Join(device.Name(), paths[signal.ID()]), entity))
		}
		if len(portSignals) == 0 {
			continue
		}
		ports, portsErr := fs.ReadDir(s.files, path.Join(device.Name(), "ports"))
		if portsErr != nil {
			for _, signal := range portSignals {
				readings = append(readings, Reading{Signal: signal, Entity: entity, Failure: Failure(portsErr)})
			}
			continue
		}
		for _, port := range ports {
			if _, parseErr := strconv.ParseUint(port.Name(), 10, 32); parseErr != nil {
				continue
			}
			for _, signal := range portSignals {
				if contextErr := ctx.Err(); contextErr != nil {
					return nil, contextErr
				}
				entity := &observation.Entity{Type: "infiniband_port", Id: path.Join(device.Name(), port.Name())}
				name := path.Join(device.Name(), "ports", port.Name(), paths[signal.ID()])
				readings = append(readings, s.readSignal(signal, name, entity))
			}
		}
	}
	return readings, nil
}

func (s *Source) readFile(name string) ([]byte, error) {
	if err := s.restricted[name]; err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(s.files, name)
	// Repeated EINVAL reads on restricted PFs generate ACCESS_REG kernel errors.
	if errors.Is(err, syscall.EINVAL) {
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
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.EOPNOTSUPP):
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, ErrInUse):
		category = observation.CollectionErrorCategoryUnavailable
	}
	return &observation.CollectionError{Category: category, Detail: err.Error()}
}

func (s *Source) readSignal(signal collect.Signal, name string, entity *observation.Entity) Reading {
	reading := Reading{Signal: signal, Entity: entity}
	raw, err := s.readFile(name)
	switch {
	case err != nil:
		reading.Failure = Failure(err)
	case strings.HasPrefix(strings.TrimSpace(string(raw)), "N/A"):
		reading.Failure = &observation.CollectionError{Category: observation.CollectionErrorCategoryUnsupported, Reason: observation.CollectionErrorReasonValueReportedUnsupported, Detail: strings.TrimSpace(string(raw))}
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
