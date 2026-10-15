// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command health-validation collects host GPU telemetry, evaluates the
// health check catalog, and prints a node-level verdict as JSON.
//
// Subcommands:
//
//	health-validation run [--node-id <id>] [--config <path>]
//	health-validation version [--short] [--output json]
//
// It is a thin shim over [app.App]: the same builder ([app.New]) lets a
// downstream consumer assemble their own binary that inherits this behavior
// while contributing their own checks and a per-GPU probe. See the toolkit/app
// package.
package main

import (
	"os"

	"github.com/dsx-ai-factory/health-validation/toolkit/app"
)

func main() {
	os.Exit(app.New().WithStderrLogging().Run(os.Args[1:], os.Stdout, os.Stderr))
}
