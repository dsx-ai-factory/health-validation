// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	collectgpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

// TestWiring covers the setup run does before its first DCGM read: the DCGM
// sensor validates the watched signals without connecting to a hostengine.
func TestWiring(t *testing.T) {
	_, err := dcgmsensor.New(dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{}), watchedSignals()...)
	require.NoError(t, err)
	_, err = newValidationCatalog("H100")
	require.NoError(t, err)
}

// TestWatchedSignalsFeedRegisteredChecks keeps signals mapped for checks this
// example does not register, such as NVML-only NVLink link signals, out of the
// DCGM watch.
func TestWatchedSignalsFeedRegisteredChecks(t *testing.T) {
	watched := watchedSignals()
	require.Contains(t, watched, collectgpu.FabricManagerStatus.ID())
	for _, id := range watched {
		required := slices.ContainsFunc(checks(), func(check health.CheckDefinition) bool {
			return slices.ContainsFunc(observations.MappedFeeds(check.Category)[id], func(s string) bool {
				return slices.Contains(check.RequiredSignals, s)
			})
		})
		require.True(t, required, "%s feeds no registered check", id)
	}
	require.NotContains(t, watched, collectgpu.NVLinkState.ID())

	// Every signal a registered check requires is watched; gpu_id comes from
	// the target.
	for _, check := range checks() {
		feeds := observations.MappedFeeds(check.Category)
		for _, signal := range check.RequiredSignals {
			if signal == "gpu_id" {
				continue
			}
			fed := slices.ContainsFunc(watched, func(id string) bool { return slices.Contains(feeds[id], signal) })
			require.True(t, fed, "%s needs %s, which no watched signal feeds", check.ID, signal)
		}
	}
}
