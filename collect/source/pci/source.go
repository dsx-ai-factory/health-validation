// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pci reads PCI sysfs attributes and bounded configuration space.
package pci

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
)

const (
	Name = "pci.sysfs"
	Root = "sys/bus/pci/devices"
)

var (
	addressPattern   = regexp.MustCompile(`^[0-9a-fA-F]{4,8}:[0-9a-fA-F]{2}:[0-1][0-9a-fA-F]\.[0-7]$`)
	ErrInUse         = errors.New("PCI read already in progress")
	ErrMalformed     = errors.New("malformed PCI configuration or attribute")
	ErrLimitedConfig = errors.New("extended PCI configuration unavailable")
)

// Source has no native library or service dependency.
type Source struct {
	files fs.FS
	mu    sync.Mutex
}

func New(files fs.FS) *Source {
	if files == nil {
		files = os.DirFS("/")
	}
	return &Source{files: files}
}

type Reading struct {
	Signal  collect.Signal
	Target  *observation.ObservationTarget
	Value   *observation.Value
	Failure *observation.CollectionError
	NoValue *observation.NoValue
	Path    string
}

// ACS contains unmodified 16-bit registers, including reserved bits.
type ACS struct{ Offset, Capability, Control uint16 }

// ParseACS walks at most 960 aligned extended-capability headers. A short
// configuration read is not proof that ACS is absent.
func ParseACS(config []byte) (*ACS, error) {
	if len(config) < 0x104 {
		return nil, fmt.Errorf("%w: only %d configuration bytes readable; cannot determine ACS availability", ErrLimitedConfig, len(config))
	}
	var visited [1024]bool
	var found *ACS
	for offset := 0x100; ; {
		if offset < 0x100 || offset > 0xffc || offset%4 != 0 || visited[offset/4] {
			return nil, fmt.Errorf("%w: invalid or cyclic capability offset %#x", ErrMalformed, offset)
		}
		visited[offset/4] = true
		if offset+4 > len(config) {
			return nil, fmt.Errorf("%w: truncated capability header at %#x", ErrMalformed, offset)
		}
		header := binary.LittleEndian.Uint32(config[offset : offset+4])
		if header == 0 {
			if offset != 0x100 {
				return nil, fmt.Errorf("%w: capability link points to an empty header at %#x", ErrMalformed, offset)
			}
			return found, nil
		}
		if header == 0xffffffff {
			return nil, fmt.Errorf("%w: invalid capability header %#x at %#x", ErrMalformed, header, offset)
		}
		if header&0xffff == 0x0d {
			if offset+8 > len(config) {
				return nil, fmt.Errorf("%w: truncated ACS registers at %#x", ErrMalformed, offset)
			}
			if found != nil {
				return nil, fmt.Errorf("%w: duplicate ACS capability", ErrMalformed)
			}
			found = &ACS{Offset: uint16(offset), Capability: binary.LittleEndian.Uint16(config[offset+4 : offset+6]), Control: binary.LittleEndian.Uint16(config[offset+6 : offset+8])}
		}
		next := int(header >> 20)
		if next == 0 {
			return found, nil
		}
		offset = next
	}
}

func (s *Source) Read(ctx context.Context, selected collect.Signals) ([]Reading, error) {
	selected, err := collect.NewOp(collect.WithSignalID(selected.IDs()...)).Signals(pcisignals.All())
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if !s.mu.TryLock() {
		return nil, ErrInUse
	}
	defer s.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	root, err := s.resolve(Root)
	if err != nil {
		return nil, err
	}
	devices, err := fs.ReadDir(s.files, root)
	if err != nil {
		return nil, err
	}
	var readings []Reading
	for _, device := range devices {
		if !addressPattern.MatchString(device.Name()) {
			continue
		}
		address := strings.ToLower(device.Name())
		target := observation.PCITarget(address)
		base := path.Join(root, device.Name())
		cache := make(map[string]numericResult)
		var acs *ACS
		var acsErr error
		acsRead := false
		for _, signal := range selected {
			reading := Reading{Signal: signal, Target: target, Path: base}
			if err := ctx.Err(); err != nil {
				reading.Failure = Failure(err)
				readings = append(readings, reading)
				continue
			}
			var value uint64
			var err error
			switch signal.ID() {
			case pcisignals.Address.ID():
				reading.Value = text(address)
			case pcisignals.Vendor.ID(), pcisignals.Device.ID(), pcisignals.Class.ID():
				field := "vendor"
				bits := 16
				if signal.ID() == pcisignals.Device.ID() {
					field = "device"
				}
				if signal.ID() == pcisignals.Class.ID() {
					field = "class"
					bits = 24
				}
				reading.Path = path.Join(base, field)
				value, err = s.number(reading.Path, bits, cache)
				if err == nil {
					reading.Value = number(value)
				}
			case pcisignals.Driver.ID(), pcisignals.Module.ID():
				reading.Path = path.Join(base, "driver")
				if signal.ID() == pcisignals.Module.ID() {
					reading.Path = path.Join(reading.Path, "module")
				}
				var resolved string
				resolved, err = s.resolve(reading.Path)
				if err == nil {
					_, err = fs.Stat(s.files, resolved)
					if err == nil {
						reading.Value = text(path.Base(resolved))
					}
				}
				if errors.Is(err, fs.ErrNotExist) {
					reading.NoValue = &observation.NoValue{Reason: observation.NoValueReasonNotSupported, Detail: "no bound driver or module"}
					err = nil
				}
			case pcisignals.GPUPresent.ID():
				reading.Path = path.Join(base, "class")
				var class, vendor uint64
				class, err = s.number(reading.Path, 24, cache)
				if err == nil && (class>>8 == 0x0300 || class>>8 == 0x0302 || class>>16 == 0x12) {
					reading.Path = path.Join(base, "vendor")
					vendor, err = s.number(reading.Path, 16, cache)
				}
				if err == nil {
					reading.Value = &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: vendor == 0x10de && (class>>8 == 0x0300 || class>>8 == 0x0302 || class>>16 == 0x12)}}
				}
			default:
				reading.Path = path.Join(base, "config")
				if !acsRead {
					acsRead = true
					var config []byte
					config, acsErr = s.readFile(reading.Path, 4096)
					if acsErr == nil {
						acs, acsErr = ParseACS(config)
					}
				}
				err = acsErr
				if err == nil && acs == nil {
					reading.NoValue = &observation.NoValue{Reason: observation.NoValueReasonNotSupported, Detail: "ACS extended capability absent"}
				}
				if err == nil && acs != nil {
					value = uint64(acs.Capability)
					if signal.ID() == pcisignals.ACSControl.ID() {
						value = uint64(acs.Control)
					}
					if signal.ID() == pcisignals.ACSOffset.ID() {
						value = uint64(acs.Offset)
					}
					reading.Value = number(value)
				}
			}
			if err != nil {
				reading.Failure = Failure(err)
			}
			if reading.Failure != nil {
				reading.Failure.Operation = reading.Path
			}
			readings = append(readings, reading)
		}
	}
	if len(readings) == 0 {
		for _, signal := range selected {
			readings = append(readings, Reading{Signal: signal, NoValue: &observation.NoValue{Reason: observation.NoValueReasonNoTargets, Detail: "no PCI devices"}, Path: Root})
		}
	}
	return readings, nil
}

type numericResult struct {
	err   error
	value uint64
}

func (s *Source) number(name string, bits int, cache map[string]numericResult) (uint64, error) {
	if result, ok := cache[name]; ok {
		return result.value, result.err
	}
	raw, err := s.readFile(name, 128)
	var value uint64
	if err == nil {
		value, err = strconv.ParseUint(strings.TrimSpace(string(raw)), 0, bits)
		if err != nil {
			err = fmt.Errorf("%w: %s: %v", ErrMalformed, name, err)
		}
	}
	cache[name] = numericResult{value: value, err: err}
	return value, err
}

func (s *Source) readFile(name string, maximum int64) ([]byte, error) {
	resolved, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	file, err := s.files.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err == nil && int64(len(raw)) > maximum {
		err = fmt.Errorf("%w: %s exceeds %d bytes", ErrMalformed, name, maximum)
	}
	return raw, err
}

// resolve follows sysfs device and driver links within the supplied filesystem.
// It rejects escapes and bounds link expansion, rather than trusting DirFS links.
func (s *Source) resolve(name string) (string, error) {
	pending := strings.Split(name, "/")
	var resolved []string
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == "." || part == "" {
			continue
		}
		if part == ".." {
			if len(resolved) == 0 {
				return "", fmt.Errorf("%w: symlink escapes filesystem root", fs.ErrPermission)
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		candidate := path.Join(append(resolved, part)...)
		entries, err := fs.ReadDir(s.files, path.Dir(candidate))
		if err != nil {
			return "", err
		}
		var entry fs.DirEntry
		for _, current := range entries {
			if current.Name() == part {
				entry = current
				break
			}
		}
		if entry == nil {
			return "", &fs.PathError{Op: "resolve", Path: candidate, Err: fs.ErrNotExist}
		}
		if entry.Type()&fs.ModeSymlink == 0 {
			resolved = append(resolved, part)
			continue
		}
		links++
		if links > 40 {
			return "", fmt.Errorf("%w: too many symlinks at %s", ErrMalformed, candidate)
		}
		link, err := fs.ReadLink(s.files, candidate)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(link, "/") {
			resolved = nil
		}
		pending = append(strings.Split(link, "/"), pending...)
	}
	return path.Join(resolved...), nil
}

func text(value string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: value}}
}

func number(value uint64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}
}

func Failure(err error) *observation.CollectionError {
	category := observation.CollectionErrorCategorySourceError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, collect.ErrCollectionTimeout):
		category = observation.CollectionErrorCategoryTimeout
	case errors.Is(err, fs.ErrPermission):
		category = observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, ErrMalformed):
		category = observation.CollectionErrorCategoryParseError
	case errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, fs.ErrInvalid):
		category = observation.CollectionErrorCategoryUnsupported
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrInUse), errors.Is(err, ErrLimitedConfig), errors.Is(err, context.Canceled), errors.Is(err, collect.ErrCollectionCanceled):
		category = observation.CollectionErrorCategoryUnavailable
	}
	return &observation.CollectionError{Category: category, Detail: err.Error()}
}
