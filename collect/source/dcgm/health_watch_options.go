// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"time"
)

// HealthWatchOptions configures requested health sampling and cache retention,
// independently of metric WatchPolicy. DCGM may enforce subsystem-specific
// settings, and other watchers may cause shared fields to be sampled faster.
// Options are copied when the source is constructed.
type HealthWatchOptions struct {
	// UpdateInterval must be positive whole microseconds; zero uses 30 seconds.
	UpdateInterval time.Duration
	// MaxKeepAge is positive cache retention; zero uses 10 minutes. Retention
	// does not guarantee that every health check has enough history.
	MaxKeepAge time.Duration
}

func freezeHealthWatchOptions(options *HealthWatchOptions) (*HealthWatchOptions, error) {
	frozen := HealthWatchOptions{UpdateInterval: 30 * time.Second, MaxKeepAge: 10 * time.Minute}
	if options != nil {
		if options.UpdateInterval != 0 {
			frozen.UpdateInterval = options.UpdateInterval
		}
		if options.MaxKeepAge != 0 {
			frozen.MaxKeepAge = options.MaxKeepAge
		}
	}
	if err := validateWatchInterval(frozen.UpdateInterval); err != nil {
		return nil, fmt.Errorf("DCGM health update interval: %w", err)
	}
	if frozen.MaxKeepAge <= 0 {
		return nil, fmt.Errorf("DCGM health retention must be positive")
	}
	return &frozen, nil
}
