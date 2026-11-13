// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

func Example() {
	// Create catalog -- all NVIDIA checks are loaded automatically.
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// Any toolkit.Sensor works. This one reports three GPUs on a GB200 node,
	// which expects four.
	sensor := staticSensor{name: "node", ms: []health.Measurement{
		node.NodeMeasurement{GPUCount: 3},
	}}

	// Collect measurements and evaluate.
	verdict, err := toolkit.Collect(cat, sensor)
	if err != nil {
		panic(err)
	}

	fmt.Printf("severity=%s\n", verdict.OverallSeverity)

	// Output:
	// severity=CRITICAL
}
