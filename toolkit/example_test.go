/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package toolkit_test

import (
	"fmt"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/toolkit"
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

	fmt.Printf("severity=%s exit_code=%d\n", verdict.OverallSeverity, verdict.ExitCode())

	// Output:
	// severity=CRITICAL exit_code=1
}
