/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_NoArgs(t *testing.T) {
	exitCode := run([]string{})
	if exitCode == 0 {
		t.Error("Expected non-zero exit code when no arguments provided")
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	exitCode := run([]string{"-invalid"})
	if exitCode == 0 {
		t.Error("Expected non-zero exit code for invalid flag")
	}
}

func TestRun_NonExistentDir(t *testing.T) {
	exitCode := run([]string{"-dir", "/nonexistent/directory"})
	if exitCode == 0 {
		t.Error("Expected non-zero exit code for non-existent directory")
	}
}

func TestRun_ValidDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "test.go")

	input := `package main

func MyFunc() {
	println("hello")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	exitCode := run([]string{"-dir", tmpDir})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}

	// Verify file was instrumented
	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, `defer tracer.Enter("MyFunc")()`) {
		t.Error("Expected function to be instrumented")
	}
}

func TestInstrumentFile_SimpleFunction(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func SimpleFunc() {
	println("hello")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Check for tracer import (without alias since it's the default)
	if !strings.Contains(outputStr, `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"`) {
		t.Error("Expected tracer import")
	}

	// Check for defer statement
	if !strings.Contains(outputStr, `defer tracer.Enter("SimpleFunc")()`) {
		t.Error("Expected defer tracer.Enter call")
	}
}

func TestInstrumentFile_MethodWithReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

type MyType struct{}

func (m *MyType) MyMethod() {
	println("method")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Check for method name with receiver
	if !strings.Contains(outputStr, `defer tracer.Enter("(*MyType).MyMethod")()`) {
		t.Error("Expected defer tracer.Enter with receiver type")
	}
}

func TestInstrumentFile_SkipInit(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func init() {
	println("init")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// init should not be instrumented
	if strings.Contains(outputStr, `defer tracer.Enter("init")()`) {
		t.Error("init function should not be instrumented")
	}
}

func TestInstrumentFile_SkipMain(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func main() {
	println("main")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// main should not be instrumented
	if strings.Contains(outputStr, `defer tracer.Enter("main")()`) {
		t.Error("main function should not be instrumented")
	}
}

func TestInstrumentFile_SkipTestFunctions(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input_test.go")

	input := `package main

import "testing"

func TestSomething(t *testing.T) {
	println("test")
}

func BenchmarkSomething(b *testing.B) {
	println("benchmark")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Test and Benchmark functions should not be instrumented
	if strings.Contains(outputStr, `defer tracer.Enter("TestSomething")()`) {
		t.Error("Test function should not be instrumented")
	}
	if strings.Contains(outputStr, `defer tracer.Enter("BenchmarkSomething")()`) {
		t.Error("Benchmark function should not be instrumented")
	}
}

func TestInstrumentFile_PreservesComments(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

// MyFunc does something important
func MyFunc() {
	// This is a comment
	println("hello")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Comments should be preserved
	if !strings.Contains(outputStr, "// MyFunc does something important") {
		t.Error("Function comment should be preserved")
	}
	if !strings.Contains(outputStr, "// This is a comment") {
		t.Error("Inline comment should be preserved")
	}
}

func TestInstrumentFile_MultipleImports(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

import (
	"fmt"
	"os"
)

func MyFunc() {
	fmt.Println("hello")
	os.Exit(0)
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Should have tracer import added (without alias)
	if !strings.Contains(outputStr, `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"`) {
		t.Error("Expected tracer import to be added")
	}

	// Original imports should be preserved
	if !strings.Contains(outputStr, `"fmt"`) {
		t.Error("Expected fmt import to be preserved")
	}
	if !strings.Contains(outputStr, `"os"`) {
		t.Error("Expected os import to be preserved")
	}
}

func TestInstrumentFile_NoImports(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func MyFunc() {
	println("hello")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Should create import declaration
	if !strings.Contains(outputStr, "import") {
		t.Error("Expected import declaration to be created")
	}
	if !strings.Contains(outputStr, `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"`) {
		t.Error("Expected tracer import")
	}
}

func TestInstrumentFile_EmptyFunction(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func EmptyFunc() {
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Even empty functions should be instrumented
	if !strings.Contains(outputStr, `defer tracer.Enter("EmptyFunc")()`) {
		t.Error("Expected empty function to be instrumented")
	}
}

func TestInstrumentFile_InterfaceMethod(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

type MyInterface interface {
	DoSomething()
}

type MyImpl struct{}

func (m *MyImpl) DoSomething() {
	println("doing")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Implementation should be instrumented
	if !strings.Contains(outputStr, `defer tracer.Enter("(*MyImpl).DoSomething")()`) {
		t.Error("Expected interface implementation to be instrumented")
	}
}

func TestShouldSkipFunction_Init(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main
func init() {}`

	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if !shouldSkipFunction(fn) {
		t.Error("init function should be skipped")
	}
}

func TestShouldSkipFunction_Main(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main
func main() {}`

	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if !shouldSkipFunction(fn) {
		t.Error("main function should be skipped")
	}
}

func TestShouldSkipFunction_TestFunction(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main
import "testing"
func TestSomething(t *testing.T) {}`

	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if !shouldSkipFunction(fn) {
		t.Error("Test function should be skipped")
	}
}

func TestShouldSkipFunction_RegularFunction(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main
func MyFunc() {}`

	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if shouldSkipFunction(fn) {
		t.Error("Regular function should not be skipped")
	}
}

func TestInstrumentFile_InvalidSyntax(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

func BrokenFunc( {
	println("broken")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err == nil {
		t.Error("Expected error for invalid syntax")
	}
}

func TestInstrumentFile_AlreadyInstrumented(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

import tracer "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"

func MyFunc() {
	defer tracer.Enter("MyFunc")()
	println("hello")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	// Should not error on already instrumented file
	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed on already instrumented file: %v", err)
	}
}

func TestFindUniqueAlias_NoConflict(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

func MyFunc() {
	println("hello")
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	alias := findUniqueAlias(node, "tracer")
	if alias != "tracer" {
		t.Errorf("Expected 'tracer', got '%s'", alias)
	}
}

func TestFindUniqueAlias_WithConflict(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

import "some/tracer"

func MyFunc() {
	tracer.DoSomething()
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	alias := findUniqueAlias(node, "tracer")
	if alias != "tracer1" {
		t.Errorf("Expected 'tracer1', got '%s'", alias)
	}
}

func TestFindUniqueAlias_MultipleConflicts(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

var tracer int
var tracer1 string
var tracer2 bool

func MyFunc() {
	println(tracer, tracer1, tracer2)
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	alias := findUniqueAlias(node, "tracer")
	if alias != "tracer3" {
		t.Errorf("Expected 'tracer3', got '%s'", alias)
	}
}

func TestGetReceiverType_Ident(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

type MyType struct{}

func (m MyType) MyMethod() {}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil || fn.Recv == nil {
		t.Fatal("Method not found")
	}

	recvType := getReceiverType(fn.Recv.List[0].Type)
	if recvType != "MyType" {
		t.Errorf("Expected 'MyType', got '%s'", recvType)
	}
}

func TestGetReceiverType_StarExpr(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

type MyType struct{}

func (m *MyType) MyMethod() {}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil || fn.Recv == nil {
		t.Fatal("Method not found")
	}

	recvType := getReceiverType(fn.Recv.List[0].Type)
	if recvType != "*MyType" {
		t.Errorf("Expected '*MyType', got '%s'", recvType)
	}
}

func TestGetReceiverType_Unknown(t *testing.T) {
	// Test with an unsupported expression type (selector expression)
	selExpr := &ast.SelectorExpr{
		X:   &ast.Ident{Name: "pkg"},
		Sel: &ast.Ident{Name: "Type"},
	}

	recvType := getReceiverType(selExpr)
	if recvType != "Unknown" {
		t.Errorf("Expected 'Unknown', got '%s'", recvType)
	}
}

func TestHasTracerCall_NoDefer(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

func MyFunc() {
	println("hello")
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if hasTracerCall(fn, "tracer") {
		t.Error("Expected no tracer call")
	}
}

func TestHasTracerCall_DifferentDefer(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

func MyFunc() {
	defer cleanup()
	println("hello")
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if hasTracerCall(fn, "tracer") {
		t.Error("Expected no tracer call")
	}
}

func TestHasTracerCall_WithTracerCall(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

import tracer "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"

func MyFunc() {
	defer tracer.Enter("MyFunc")()
	println("hello")
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	if !hasTracerCall(fn, "tracer") {
		t.Error("Expected tracer call to be detected")
	}
}

func TestHasTracerCall_WrongAlias(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

import tracer1 "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"

func MyFunc() {
	defer tracer1.Enter("MyFunc")()
	println("hello")
}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(node, func(n ast.Node) bool {
		if f, ok := n.(*ast.FuncDecl); ok {
			fn = f

			return false
		}

		return true
	})

	if fn == nil {
		t.Fatal("Function not found")
	}

	// Looking for "tracer" but code uses "tracer1"
	if hasTracerCall(fn, "tracer") {
		t.Error("Expected no tracer call with wrong alias")
	}

	// Should find it with correct alias
	if !hasTracerCall(fn, "tracer1") {
		t.Error("Expected tracer call with correct alias")
	}
}

func TestAddTracerCall_NilBody(t *testing.T) {
	fn := &ast.FuncDecl{
		Name: &ast.Ident{Name: "MyFunc"},
		Body: nil,
	}

	addTracerCall(fn, "tracer")

	if fn.Body == nil {
		t.Error("Expected body to be created")
	}

	if len(fn.Body.List) != 1 {
		t.Errorf("Expected 1 statement, got %d", len(fn.Body.List))
	}
}

func TestAddTracerCall_WithReceiver(t *testing.T) {
	fn := &ast.FuncDecl{
		Name: &ast.Ident{Name: "MyMethod"},
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Type: &ast.StarExpr{
						X: &ast.Ident{Name: "MyType"},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{},
		},
	}

	addTracerCall(fn, "tracer")

	if len(fn.Body.List) != 1 {
		t.Errorf("Expected 1 statement, got %d", len(fn.Body.List))
	}

	deferStmt, ok := fn.Body.List[0].(*ast.DeferStmt)
	if !ok {
		t.Fatal("Expected defer statement")
	}

	callExpr, ok := deferStmt.Call.Fun.(*ast.CallExpr)
	if !ok {
		t.Fatal("Expected call expression")
	}

	// Check the function name includes receiver
	if len(callExpr.Args) != 1 {
		t.Fatal("Expected 1 argument")
	}

	lit, ok := callExpr.Args[0].(*ast.BasicLit)
	if !ok {
		t.Fatal("Expected basic literal")
	}

	if !strings.Contains(lit.Value, "(*MyType).MyMethod") {
		t.Errorf("Expected receiver in function name, got %s", lit.Value)
	}
}

func TestAddTracerImport_WithAlias(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

func MyFunc() {}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	addTracerImport(node, "tracer1")

	// Check that import was added with alias
	found := false
	for _, imp := range node.Imports {
		if imp.Path.Value == `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"` {
			found = true
			if imp.Name == nil || imp.Name.Name != "tracer1" {
				t.Error("Expected import to have alias 'tracer1'")
			}
		}
	}

	if !found {
		t.Error("Expected tracer import to be added")
	}
}

func TestAddTracerImport_NoAlias(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

func MyFunc() {}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	addTracerImport(node, "tracer")

	// Check that import was added without alias
	found := false
	for _, imp := range node.Imports {
		if imp.Path.Value == `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"` {
			found = true
			if imp.Name != nil {
				t.Error("Expected import to have no alias")
			}
		}
	}

	if !found {
		t.Error("Expected tracer import to be added")
	}
}

func TestAddTracerImport_ExistingImports(t *testing.T) {
	fset := token.NewFileSet()
	code := `package main

import (
	"fmt"
	"os"
)

func MyFunc() {}
`
	node, err := parser.ParseFile(fset, "", code, 0)
	if err != nil {
		t.Fatal(err)
	}

	originalImportCount := len(node.Imports)
	addTracerImport(node, "tracer")

	if len(node.Imports) != originalImportCount+1 {
		t.Errorf("Expected %d imports, got %d", originalImportCount+1, len(node.Imports))
	}

	// Verify tracer import was added
	found := false
	for _, imp := range node.Imports {
		if imp.Path.Value == `"github.com/LFDT-Panurus/panurus/tools/profiler/tracer"` {
			found = true
		}
	}

	if !found {
		t.Error("Expected tracer import to be added")
	}
}

func TestInstrumentFile_ValueReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.go")

	input := `package main

type MyType struct{}

func (m MyType) MyMethod() {
	println("method")
}
`

	if err := os.WriteFile(inputFile, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	err := instrumentFile(inputFile)
	if err != nil {
		t.Fatalf("instrumentFile failed: %v", err)
	}

	output, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(output)

	// Check for method name with value receiver (no pointer)
	if !strings.Contains(outputStr, `defer tracer.Enter("(MyType).MyMethod")()`) {
		t.Error("Expected defer tracer.Enter with value receiver type")
	}
}
