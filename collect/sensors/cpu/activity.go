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

func (s *sensor) readActivity(ctx context.Context, selected collect.Signals, now *timestamppb.Timestamp) map[string]*observation.Observation {
	out := make(map[string]*observation.Observation)
	definitions := cpusignals.Activity()
	for _, group := range []struct {
		path       string
		start, end int
	}{{"proc/stat", 0, 10}, {"proc/loadavg", 10, 13}} {
		var wanted collect.Signals
		for _, definition := range selected {
			for _, candidate := range definitions[group.start:group.end] {
				if definition == candidate {
					wanted = append(wanted, definition)
					break
				}
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
			if group.path == "proc/stat" {
				for _, line := range strings.Split(string(data), "\n") {
					row := strings.Fields(line)
					if len(row) > 0 && row[0] == "cpu" {
						fields = row[1:]
						break
					}
				}
			} else {
				fields = strings.Fields(string(data))
			}
		}
		for _, definition := range wanted {
			if readErr != nil {
				out[definition.ID()] = failure(definition, now, "read /"+group.path, readCategory(readErr), readErr)
				continue
			}
			index := 0
			for i, candidate := range definitions[group.start:group.end] {
				if candidate == definition {
					index = i
					break
				}
			}
			var value float64
			var err error
			category := observation.CollectionErrorCategoryParseError
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
