/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package main provides automatic code instrumentation for hierarchical call tracing.
//
// This tool walks through Go source files and automatically adds tracer hooks
// to function entries, enabling detailed performance profiling and call hierarchy analysis.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is the main logic, extracted for testability
func run(args []string) int {
	fs := flag.NewFlagSet("auto-instrument", flag.ContinueOnError)
	dir := fs.String("dir", "", "Directory to instrument (required)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)

		return 1
	}

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "Usage: auto-instrument -dir <directory>")

		return 1
	}

	if err := instrumentDirectory(*dir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		return 1
	}

	fmt.Println("Instrumentation complete!")

	return 0
}

// instrumentDirectory walks through all Go files in the directory and instruments them.
func instrumentDirectory(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories, non-Go files, and test files
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fmt.Printf("Processing: %s\n", path)

		return instrumentFile(path)
	})
}

// instrumentFile adds tracer hooks to all eligible functions in a Go source file.
func instrumentFile(filename string) error {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	modified := false
	hasTracerImport := false
	tracerAlias := "tracer"

	// Check if tracer is already imported
	for _, imp := range node.Imports {
		if imp.Path.Value == `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"` {
			hasTracerImport = true
			if imp.Name != nil {
				tracerAlias = imp.Name.Name
			}

			break
		}
	}

	// Check if any functions need instrumentation
	needsTracer := false
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if shouldSkipFunction(fn) {
			return true
		}

		needsTracer = true

		return true
	})

	if !needsTracer {
		return nil
	}

	// Add tracer import if needed
	if !hasTracerImport {
		tracerAlias = findUniqueAlias(node, "tracer")
		addTracerImport(node, tracerAlias)
		modified = true
	}

	// Add tracer calls to functions
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if shouldSkipFunction(fn) {
			return true
		}

		if !hasTracerCall(fn, tracerAlias) {
			addTracerCall(fn, tracerAlias)
			modified = true
		}

		return true
	})

	if !modified {
		return nil
	}

	// Write the modified file
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return err
	}

	return os.WriteFile(filename, buf.Bytes(), 0644)
}

// findUniqueAlias finds a unique identifier name to avoid conflicts.
func findUniqueAlias(node *ast.File, base string) string {
	usedNames := make(map[string]bool)

	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			usedNames[ident.Name] = true
		}

		return true
	})

	if !usedNames[base] {
		return base
	}

	for i := 1; ; i++ {
		alias := fmt.Sprintf("%s%d", base, i)
		if !usedNames[alias] {
			return alias
		}
	}
}

// addTracerImport adds the tracer package import to the file.
func addTracerImport(node *ast.File, alias string) {
	importSpec := &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"`,
		},
	}

	if alias != "tracer" {
		importSpec.Name = &ast.Ident{Name: alias}
	}

	if len(node.Imports) == 0 {
		node.Decls = append([]ast.Decl{
			&ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{importSpec},
			},
		}, node.Decls...)
	} else {
		for _, decl := range node.Decls {
			if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
				genDecl.Specs = append(genDecl.Specs, importSpec)

				break
			}
		}
	}

	node.Imports = append(node.Imports, importSpec)
}

// shouldSkipFunction determines if a function should not be instrumented.
// Skips: init, main, Test*, Benchmark* functions.
func shouldSkipFunction(fn *ast.FuncDecl) bool {
	name := fn.Name.Name

	return name == "init" || name == "main" || strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark")
}

// hasTracerCall checks if a function already has a tracer hook at the beginning.
func hasTracerCall(fn *ast.FuncDecl, tracerAlias string) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}

	firstStmt := fn.Body.List[0]
	deferStmt, ok := firstStmt.(*ast.DeferStmt)
	if !ok {
		return false
	}

	callExpr, ok := deferStmt.Call.Fun.(*ast.CallExpr)
	if !ok {
		return false
	}

	selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ident, ok := selExpr.X.(*ast.Ident)

	return ok && ident.Name == tracerAlias && selExpr.Sel.Name == "Enter"
}

// addTracerCall adds a tracer hook at the beginning of a function.
// The hook format is: defer tracer.Enter("FunctionName")()
func addTracerCall(fn *ast.FuncDecl, tracerAlias string) {
	if fn.Body == nil {
		fn.Body = &ast.BlockStmt{}
	}

	funcName := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recvType := getReceiverType(fn.Recv.List[0].Type)
		funcName = fmt.Sprintf("(%s).%s", recvType, funcName)
	}

	deferStmt := &ast.DeferStmt{
		Call: &ast.CallExpr{
			Fun: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   &ast.Ident{Name: tracerAlias},
					Sel: &ast.Ident{Name: "Enter"},
				},
				Args: []ast.Expr{
					&ast.BasicLit{
						Kind:  token.STRING,
						Value: fmt.Sprintf(`"%s"`, funcName),
					},
				},
			},
		},
	}

	fn.Body.List = append([]ast.Stmt{deferStmt}, fn.Body.List...)
}

// getReceiverType extracts the receiver type name from a method.
func getReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + getReceiverType(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return "Unknown"
	}
}
