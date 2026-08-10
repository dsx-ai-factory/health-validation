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
