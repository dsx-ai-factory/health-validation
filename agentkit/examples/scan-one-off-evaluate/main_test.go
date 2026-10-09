// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

// TestWiring covers the setup run does before its first DCGM read: the DCGM
// sensor validates the watched signals without connecting to a hostengine.
func TestWiring(t *testing.T) {
	_, err := dcgmsensor.New(dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{}), watchedSignals()...)
	require.NoError(t, err)
	_, err = newValidationCatalog("H100")
	require.NoError(t, err)
}
