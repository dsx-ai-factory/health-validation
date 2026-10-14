// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command healthdump prints all builtin health check definitions as JSON.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dsx-ai-factory/health-validation/catalog"
)

func main() {
	out := flag.String("out", "", "output JSON file (default: stdout)")
	flag.Parse()

	cat := catalog.New()
	checks := cat.List(catalog.Filter{})

	b, err := json.MarshalIndent(checks, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	b = append(b, '\n')

	if *out != "" {
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
			os.Exit(1)
		}
	} else {
		os.Stdout.Write(b)
	}
}
