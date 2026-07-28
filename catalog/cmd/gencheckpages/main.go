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

// gencheckpages reads checks.json and schema.json and generates MDX pages
// for each health check category, plus a sidebar.ts for Docusaurus integration.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type check struct {
	Category string `json:"category"`
}

type enumValue struct {
	Name        string `json:"name"`
	JSON        string `json:"json"`
	Description string `json:"description"`
}

type schema struct {
	Enums map[string]struct {
		Values []enumValue `json:"values"`
	} `json:"enums"`
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: gencheckpages <checks.json> <schema.json> <outdir>\n")
		os.Exit(1)
	}
	checksPath, schemaPath, outDir := os.Args[1], os.Args[2], os.Args[3]

	checksData, err := os.ReadFile(checksPath)
	if err != nil {
		fatal(err)
	}
	var checks []check
	if err = json.Unmarshal(checksData, &checks); err != nil {
		fatal(err)
	}

	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		fatal(err)
	}
	var s schema
	if err = json.Unmarshal(schemaData, &s); err != nil {
		fatal(err)
	}

	// Count checks per category.
	counts := map[string]int{}
	for _, c := range checks {
		counts[c.Category]++
	}

	// Get categories from schema enum (preserves authored order).
	categories := s.Enums["Category"].Values

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}

	// Write _category_.json for Docusaurus sidebar grouping.
	writeFile(filepath.Join(outDir, "_category_.json"), `{
  "label": "Check Catalog",
  "position": 1,
  "link": {
    "type": "generated-index",
    "description": "Health check definitions grouped by hardware domain."
  }
}
`)

	// Write one MDX page per category.
	for i, cat := range categories {
		mdx := fmt.Sprintf(`---
sidebar_position: %d
sidebar_label: %s
---

import CheckCatalog from "@site/src/components/CheckCatalog";

# %s Checks

%s

<CheckCatalog category="%s" />
`, i+1, cat.Name, cat.Name, escapeForMDX(cat.Description), cat.JSON)

		writeFile(filepath.Join(outDir, cat.JSON+".mdx"), mdx)
	}

	// Write sidebar.ts consumed by docs/sidebars.ts.
	var items []string
	for _, cat := range categories {
		items = append(items, fmt.Sprintf(
			`    { type: "doc", id: "reference/checks/%s", label: "%s" }`,
			cat.JSON, cat.Name,
		))
	}
	sidebar := fmt.Sprintf(`import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  checksSidebar: [
    {
      type: "category",
      label: "Check Catalog",
      link: { type: "generated-index", description: "Health check definitions grouped by hardware domain." },
      items: [
%s,
      ],
    },
  ],
};

export default sidebar.checksSidebar;
`, strings.Join(items, ",\n"))

	writeFile(filepath.Join(outDir, "sidebar.ts"), sidebar)
}

func escapeForMDX(s string) string {
	s = strings.ReplaceAll(s, "{", "\\{")
	s = strings.ReplaceAll(s, "}", "\\}")
	return s
}

func writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gencheckpages: %v\n", err)
	os.Exit(1)
}
