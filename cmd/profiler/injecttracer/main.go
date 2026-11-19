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

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func main() {
	benchFile := flag.String("file", "", "Path to benchmark test file")
	benchName := flag.String("bench", "", "Benchmark function name")
	flag.Parse()

	if *benchFile == "" || *benchName == "" {
		fmt.Println("Usage: go run ./injecttracer -file <path> -bench <name>")
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
		return errors.Wrap(err, "failed to parse file")
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
		return errors.Errorf("benchmark function %s not found", benchName)
	}

	// Write back to file
	f, err := os.Create(filename)
	if err != nil {
		return errors.Wrap(err, "failed to create file")
	}

	if err := printer.Fprint(f, fset, file); err != nil {
		_ = f.Close()

		return errors.Wrap(err, "failed to write file")
	}

	// os.Create truncated the file: a failed Close can mean a partial write.
	if err := f.Close(); err != nil {
		return errors.Wrap(err, "failed to write file")
	}

	return nil
}

func addTracerImport(file *ast.File) {
	var newImports []ast.Spec
	for _, path := range []string{"github.com/LFDT-Panurus/panurus/cmd/profiler/tracer", "flag"} {
		if !hasImport(file, path) {
			newImports = append(newImports, &ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: `"` + path + `"`,
				},
			})
		}
	}

	if len(newImports) == 0 {
		return
	}

	importDecl := findImportDecl(file)
	if importDecl == nil {
		// Create new import declaration and insert it at the beginning of declarations
		importDecl = &ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: newImports,
		}
		file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
	} else {
		importDecl.Specs = append(importDecl.Specs, newImports...)
	}
}

// hasImport reports whether file imports the package at path.
func hasImport(file *ast.File, path string) bool {
	for _, imp := range file.Imports {
		if imp.Path.Value == `"`+path+`"` {
			return true
		}
	}

	return false
}

// findImportDecl returns the first import declaration of file, or nil if there is none.
func findImportDecl(file *ast.File) *ast.GenDecl {
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			return gen
		}
	}

	return nil
}

func addFlagVars(file *ast.File) {
	if hasFlagVars(file) {
		return
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

	// Insert flag declarations after imports
	insertPos := leadingGenDeclCount(file)
	for _, decl := range flagFile.Decls {
		file.Decls = append(file.Decls[:insertPos], append([]ast.Decl{decl}, file.Decls[insertPos:]...)...)
		insertPos++
	}
}

// hasFlagVars reports whether file already declares any of the flag variables addFlagVars adds.
func hasFlagVars(file *ast.File) bool {
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR && declaresFlagVar(gen) {
			return true
		}
	}

	return false
}

// declaresFlagVar reports whether the var declaration gen names any of the flag variables
// addFlagVars adds.
func declaresFlagVar(gen *ast.GenDecl) bool {
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range vs.Names {
			switch name.Name {
			case "showTime", "showPercent", "rootFunction", "minPercent":
				return true
			}
		}
	}

	return false
}

// leadingGenDeclCount returns the number of consecutive generic declarations (imports, consts,
// types, vars) at the start of file.
func leadingGenDeclCount(file *ast.File) int {
	for i, decl := range file.Decls {
		if _, ok := decl.(*ast.GenDecl); !ok {
			return i
		}
	}

	return len(file.Decls)
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
