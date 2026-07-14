//go:build tools

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
)

func main() {
	benchFile := flag.String("file", "", "Path to benchmark test file")
	benchName := flag.String("bench", "", "Benchmark function name")
	flag.Parse()

	if *benchFile == "" || *benchName == "" {
		fmt.Println("Usage: go run inject-tracer.go -file <path> -bench <name>")
		os.Exit(1)
	}

	if err := injectTracer(*benchFile, *benchName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func injectTracer(filename, benchName string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse file: %w", err)
	}

	// Add tracer import if not present
	addTracerImport(file)

	// Add flag variables at package level
	addFlagVars(file)

	// Modify the benchmark function
	modified := false
	ast.Inspect(file, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == benchName {
			injectTracerCode(fn)
			modified = true
			return false
		}
		return true
	})

	if !modified {
		return fmt.Errorf("benchmark function %s not found", benchName)
	}

	// Write back to file
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if err := printer.Fprint(f, fset, file); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func addTracerImport(file *ast.File) {
	tracerPath := "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"
	flagPath := "flag"

	// Check if imports already exist
	hasTracer := false
	hasFlag := false
	for _, imp := range file.Imports {
		if imp.Path.Value == `"`+tracerPath+`"` {
			hasTracer = true
		}
		if imp.Path.Value == `"`+flagPath+`"` {
			hasFlag = true
		}
	}

	if hasTracer && hasFlag {
		return
	}

	// Add tracer import
	var newImports []ast.Spec
	if !hasTracer {
		newImports = append(newImports, &ast.ImportSpec{
			Path: &ast.BasicLit{
				Kind:  token.STRING,
				Value: `"` + tracerPath + `"`,
			},
		})
	}

	// Add flag import
	if !hasFlag {
		newImports = append(newImports, &ast.ImportSpec{
			Path: &ast.BasicLit{
				Kind:  token.STRING,
				Value: `"` + flagPath + `"`,
			},
		})
	}

	if len(newImports) == 0 {
		return
	}

	// Find or create import declaration
	var importDecl *ast.GenDecl
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			importDecl = gen
			break
		}
	}

	if importDecl == nil {
		// Create new import declaration
		importDecl = &ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: newImports,
		}
		// Insert at beginning of declarations
		file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
	} else {
		// Add to existing import declaration
		importDecl.Specs = append(importDecl.Specs, newImports...)
	}
}

func addFlagVars(file *ast.File) {
	// Check if flags already exist
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
			for _, spec := range gen.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vs.Names {
						if name.Name == "showTime" || name.Name == "showPercent" ||
							name.Name == "rootFunction" || name.Name == "minPercent" {
							return // Flags already exist
						}
					}
				}
			}
		}
	}

	// Create flag variable declarations
	flagVars := `
var (
	showTime     = flag.Bool("show-time", true, "Show absolute time")
	showPercent  = flag.Bool("show-percent", true, "Show percentages")
	rootFunction = flag.String("root-function", "", "Root function to start from")
	minPercent   = flag.Float64("min-percent", 0, "Minimum percentage to display")
)
`

	// Parse the flag declarations
	fset := token.NewFileSet()
	flagFile, err := parser.ParseFile(fset, "", "package main\n"+flagVars, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to parse flag declarations: %v\n", err)
		return
	}

	// Add flag declarations after imports
	insertPos := 0
	for i, decl := range file.Decls {
		if _, ok := decl.(*ast.GenDecl); ok {
			insertPos = i + 1
		} else {
			break
		}
	}

	// Insert flag declarations
	for _, decl := range flagFile.Decls {
		file.Decls = append(file.Decls[:insertPos], append([]ast.Decl{decl}, file.Decls[insertPos:]...)...)
		insertPos++
	}
}

func injectTracerCode(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}

	// Create tracer initialization code
	initCode := `
	flag.Parse()
	tracer.Enable()
	defer func() {
		tracer.Disable()
		tracer.PrintWithOptions(tracer.PrintOptions{
			RootFunction:   *rootFunction,
			ShowPercent:    *showPercent,
			ShowAbsolute:   *showTime,
			AggregateLoops: true,
			MinPercentage:  *minPercent,
		})
	}()
`

	// Parse the initialization code
	fset := token.NewFileSet()
	initFile, err := parser.ParseFile(fset, "", "package main\nfunc dummy() {"+initCode+"}", 0)
	if err != nil {
		return
	}

	// Extract statements from parsed code
	var initStmts []ast.Stmt
	ast.Inspect(initFile, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "dummy" {
			initStmts = fn.Body.List
			return false
		}
		return true
	})

	// Insert at the beginning of the benchmark function
	fn.Body.List = append(initStmts, fn.Body.List...)
}
