// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Linux exports /proc/stat CPU times in USER_HZ units (100 ticks per second),
// independent of the kernel's configured scheduler frequency.
const userHZ = 100

// Field indexes follow the proc file formats, not Activity's catalog order.
var activitySources = []struct {
	fields map[string]int
	path   string
}{
	{fields: map[string]int{
		cpusignals.UserSeconds.ID():      0,
		cpusignals.NiceSeconds.ID():      1,
		cpusignals.SystemSeconds.ID():    2,
		cpusignals.IdleSeconds.ID():      3,
		cpusignals.IOWaitSeconds.ID():    4,
		cpusignals.IRQSeconds.ID():       5,
		cpusignals.SoftIRQSeconds.ID():   6,
		cpusignals.StealSeconds.ID():     7,
		cpusignals.GuestSeconds.ID():     8,
		cpusignals.GuestNiceSeconds.ID(): 9,
	}, path: "proc/stat"},
	{fields: map[string]int{
		cpusignals.Load1.ID():  0,
		cpusignals.Load5.ID():  1,
		cpusignals.Load15.ID(): 2,
	}, path: "proc/loadavg"},
}

func (s *sensor) readActivity(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp) map[string]*observation.Observation {
	out := make(map[string]*observation.Observation)
	for _, group := range activitySources {
		var wanted collect.Signals
		for _, definition := range selected {
			if _, ok := group.fields[definition.ID()]; ok {
				wanted = append(wanted, definition)
			}
		}
		if len(wanted) == 0 {
			continue
		}
		if ctx.Err() != nil {
			return out
		}
		data, readErr := fs.ReadFile(s.fsys, group.path)
		var fields []string
		if readErr == nil {
			fields = parseActivityFields(group.path, data)
		}
		for _, definition := range wanted {
			if readErr != nil {
				out[definition.ID()] = failure(definition, now, "read /"+group.path, readCategory(readErr), readErr)
				continue
			}
			index := group.fields[definition.ID()]
			var value float64
			var err error
			category := observation.CollectionErrorCategoryParseError
			// user, nice, system and idle are the original mandatory stat fields.
			// Later kernels appended the remaining modes; their absence is
			// unsupported, whereas truncating the original four is a parse error.
			if len(fields) == 0 || (group.path == "proc/stat" && len(fields) < 4) || (group.path == "proc/loadavg" && len(fields) < 3) {
				err = fmt.Errorf("missing or truncated CPU activity data")
			} else if index >= len(fields) {
				err = fmt.Errorf("kernel does not expose field %s", definition.ID())
				category = observation.CollectionErrorCategoryUnsupported
			} else if group.path == "proc/stat" {
				var ticks uint64
				ticks, err = strconv.ParseUint(fields[index], 10, 64)
				value = float64(ticks) / userHZ
			} else {
				value, err = strconv.ParseFloat(fields[index], 64)
				if err == nil && (math.IsNaN(value) || math.IsInf(value, 0) || value < 0) {
					err = fmt.Errorf("invalid load %q", fields[index])
				}
			}
			if err != nil {
				out[definition.ID()] = failure(definition, now, "parse /"+group.path, category, err)
				continue
			}
			out[definition.ID()] = &observation.Observation{
				SignalId: definition.ID(), Source: ID,
				Target: observation.NodeTarget(""), ObservedAt: now, Unit: definition.Unit(),
				Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: value}}},
			}
		}
	}
	return out
}

func parseActivityFields(path string, data []byte) []string {
	if path == "proc/stat" {
		for line := range strings.SplitSeq(string(data), "\n") {
			row := strings.Fields(line)
			if len(row) > 0 && row[0] == "cpu" {
				return row[1:]
			}
		}
		return nil
	}
	return strings.Fields(string(data))
}
