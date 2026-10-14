// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

func Example() {
	// Create catalog -- all NVIDIA checks are loaded automatically.
	cat := catalog.New(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	// Create an Xid sensor with mock journal lines. Xid 79 ("GPU has
	// fallen off the bus") is a critical-severity Xid code.
	sensor := toolkit.NewXidSensor(toolkit.MockXid(
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
	))

	// Collect measurements and evaluate.
	verdict, err := toolkit.Collect(cat, sensor)
	if err != nil {
		panic(err)
	}

	fmt.Printf("severity=%s\n", verdict.OverallSeverity)

	// Output:
	// severity=CRITICAL
}
