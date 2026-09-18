// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// SPDX-FileCopyrightText: Copyright 2016 Euan Kemp
// Adapted from github.com/leptonai/gpud/pkg/kmsg/watcher.go (Apache-2.0),
// derived from github.com/euank/go-kmsg-parser.

// Package kmsg provides kernel record parsing and snapshot reads for sensors.
// It has no event store, content deduplication, or health policy.
package kmsg

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrMalformedRecord identifies a kernel record that could not be parsed.
var ErrMalformedRecord = errors.New("malformed kernel record")

// Message preserves the kernel record identity, priority and multiline body.
// Sequence is boot-scoped. BootID is populated by ReadAll, not Parse.
type Message struct {
	Timestamp time.Time
	BootID    string
	Body      string
	Sequence  int
	Priority  int
}

// Parse decodes one /dev/kmsg record. Additional header fields are allowed.
// The timestamp is microseconds since boot, relative to the supplied origin.
func Parse(bootTime time.Time, record string) (Message, error) {
	msg, err := parseLine(bootTime, record)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrMalformedRecord, err)
	}
	return *msg, nil
}

// Copied from GPUd watcher.go. Message field types/names, error field labels, and the
// duration overflow guard differ; Parse supplies our public error classification.
func parseLine(bootTime time.Time, line string) (*Message, error) {
	// Format:
	//   PRIORITY,SEQUENCE_NUM,TIMESTAMP,-;MESSAGE
	parts := strings.SplitN(line, ";", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid kmsg; must contain a ';'")
	}

	metadata, message := parts[0], parts[1]

	metadataParts := strings.Split(metadata, ",")
	if len(metadataParts) < 3 {
		return nil, fmt.Errorf("invalid kmsg: must contain at least 3 ',' separated pieces at the start")
	}

	priority, sequence, timestamp := metadataParts[0], metadataParts[1], metadataParts[2]

	prioNum, err := strconv.Atoi(priority)
	if err != nil {
		return nil, fmt.Errorf("could not parse %q as priority: %v", priority, err)
	}

	sequenceNum, err := strconv.Atoi(sequence)
	if err != nil {
		return nil, fmt.Errorf("could not parse %q as sequence number: %v", sequence, err)
	}

	timestampUsFromBoot, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("could not parse %q as timestamp: %v", timestamp, err)
	}
	// Keep the upstream parser, but reject multiplication overflow at our boundary.
	if timestampUsFromBoot > math.MaxInt64/int64(time.Microsecond) || timestampUsFromBoot < math.MinInt64/int64(time.Microsecond) {
		return nil, fmt.Errorf("timestamp %q overflows time.Duration", timestamp)
	}
	// timestamp is offset in microsecond from boottime.
	msgTime := bootTime.Add(time.Duration(timestampUsFromBoot) * time.Microsecond)

	return &Message{
		Priority:  prioNum,
		Sequence:  sequenceNum,
		Timestamp: msgTime,
		Body:      message,
	}, nil
}
