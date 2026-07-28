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

// Command go-health-validation collects host GPU telemetry, evaluates the
// health check catalog, and prints a node-level verdict as JSON.
//
// Subcommands:
//
//	go-health-validation run [--node-id <id>] [--config <path>]
//	go-health-validation version [--short] [--output json]
//
// It is a thin shim over [app.App]: the same builder ([app.New]) lets a
// downstream consumer assemble their own binary that inherits this behavior
// while contributing their own checks and a per-GPU probe. See the toolkit/app
// package.
package main

import (
	"os"

	"github.com/NVIDIA/health-validation/toolkit/app"
)

func main() {
	os.Exit(app.New().WithStderrLogging().Run(os.Args[1:], os.Stdout, os.Stderr))
}
