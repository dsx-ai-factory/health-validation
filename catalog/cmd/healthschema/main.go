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

// Command healthschema extracts enum and struct metadata from Go source
// files using the Go AST. It outputs a JSON schema with descriptions
// derived from source comments.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

func main() {
	out := flag.String("out", "", "output JSON file (default: stdout)")
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "usage: healthschema [-out=file.json] file1.go [file2.go ...]\n")
		os.Exit(1)
	}

	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
			os.Exit(1)
		}
		parsed = append(parsed, f)
	}

	schema := extract(parsed)

	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
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
