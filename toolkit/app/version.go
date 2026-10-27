// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/dsx-ai-factory/health-validation/internal/version"
)

// versionCommand prints build-time version metadata. The default output is a
// single human-readable line; --short prints just the version string (handy
// for scripts and the CI release smoke test), and --output json prints the
// full [version.Info] including the untruncated commit SHA. prog is the
// program name used in error-message prefixes.
func versionCommand(args []string, stdout, stderr io.Writer, prog string) int {
	fs := flag.NewFlagSet(prog+" version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	short := fs.Bool("short", false, "print only the version string")
	output := fs.String("output", "", `output format: "json" (default: human-readable)`)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *short && *output != "" {
		fmt.Fprintf(stderr, "%s: --short and --output are mutually exclusive\n", prog)
		return exitError
	}

	info := version.Get()

	switch {
	case *short:
		fmt.Fprintln(stdout, info.Version)
	case *output == "json":
		b, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "%s: marshal version: %v\n", prog, err)
			return exitError
		}
		fmt.Fprintln(stdout, string(b))
	case *output != "":
		fmt.Fprintf(stderr, "%s: unknown version output %q (want \"json\")\n", prog, *output)
		return exitError
	default:
		fmt.Fprintln(stdout, info.String())
	}
	return 0
}
