/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// The tests in this file enforce the no-bypass rule stated in
// docs/security/store_integrity_verification.md: the checks in this package are
// unconditional, and nothing — no functional option, no setter, no configuration
// key — may be added that turns one of them off. A security check a deployment
// can disable is not a security posture, and a check that is disabled by default
// in some deployment is worse than no check at all, because the contract clauses
// on the store interfaces claim it holds.
//
// The rule is enforced by reading the source, rather than by convention, because
// the failure it guards against is a future well-meaning change ("make this
// opt-in so it does not break my deployment") that no behavioural test would
// catch.
package integrity_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkedPackages are the packages that apply the checks in this package. A
// bypass would have to be introduced in one of them, or in this package itself.
var checkedPackages = []string{
	".",
	"../ttxdb",
	"../auditdb",
	"../endorserdb",
	"../db/sql/common",
	"../db/kvs",
	"../../identity",
	"../../identity/wallet",
	"../../ttx",
	"../../..",
}

// bypassNames matches identifiers that would name a way to skip verification.
// It is deliberately broad: the point is to fail on the attempt, and a rename to
// something this does not match is a conscious act rather than an oversight. Add
// to it rather than narrowing it.
var bypassNames = regexp.MustCompile(
	`(?i)(skip|disable|without|no|bypass|unsafe|unchecked|ignore)_?` +
		`(integrity|verification|verify|check|validation|validate)`,
)

// parsePackageDir parses the non-test Go files of one package directory.
func parsePackageDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "cannot read package directory [%s]", dir)

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, err, "cannot parse [%s]", name)
		files = append(files, file)
	}
	require.NotEmpty(t, files, "no source files found in [%s]", dir)

	return fset, files
}

// TestNoBypassIdentifiers asserts that no package applying the integrity checks
// declares anything named like a way to turn them off — no function, method,
// type, field, variable, or constant.
func TestNoBypassIdentifiers(t *testing.T) {
	for _, dir := range checkedPackages {
		t.Run(dir, func(t *testing.T) {
			fset, files := parsePackageDir(t, dir)
			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					ident, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					assert.False(t, bypassNames.MatchString(ident.Name),
						"%s: identifier [%s] names a way to skip verification; the checks in "+
							"token/services/storage/integrity are unconditional by design — see "+
							"docs/security/store_integrity_verification.md",
						fset.Position(ident.Pos()), ident.Name)

					return true
				})
			}
		})
	}
}

// TestChecksTakeNoOptions asserts that the exported checks of this package are
// plain functions of their inputs: not variadic, and returning only an error.
// A variadic parameter is how an option that weakens a check would be added
// without changing any call site, so it must not exist in the first place.
func TestChecksTakeNoOptions(t *testing.T) {
	fset, files := parsePackageDir(t, ".")

	found := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !strings.HasPrefix(fn.Name.Name, "Check") {
				continue
			}
			found++

			for _, param := range fn.Type.Params.List {
				_, variadic := param.Type.(*ast.Ellipsis)
				assert.False(t, variadic,
					"%s: %s takes a variadic parameter; the checks must not be configurable",
					fset.Position(param.Pos()), fn.Name.Name)
			}

			require.NotNil(t, fn.Type.Results, "%s must return an error", fn.Name.Name)
			assert.Len(t, fn.Type.Results.List, 1,
				"%s must return an error and nothing else, so that a caller cannot ignore the "+
					"verdict while still using a result", fn.Name.Name)
		}
	}
	assert.GreaterOrEqual(t, found, 6, "expected to have found the exported Check functions")
}

// TestChecksExposeNoMutableState asserts that this package holds no mutable
// package-level state. Anything settable at runtime — a flag, a hook, a
// replaceable function value — is a bypass, whether or not it is named like one.
// The only package-level values allowed are the sentinel errors, which are
// compared against and never assigned.
func TestChecksExposeNoMutableState(t *testing.T) {
	fset, files := parsePackageDir(t, ".")

	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					assert.True(t, strings.HasPrefix(name.Name, "Err") || strings.HasPrefix(name.Name, "err"),
						"%s: package-level variable [%s] is not a sentinel error; the integrity "+
							"package must hold no state a deployment could change",
						fset.Position(name.Pos()), name.Name)
				}
			}
		}
	}
}

// TestNoVerificationConfigKey asserts that no configuration key read by the
// storage layer controls verification. The keys are declared as constants, so
// this reads them from the source rather than from a hand-maintained list that
// would drift.
func TestNoVerificationConfigKey(t *testing.T) {
	for _, dir := range []string{"../db/sql/common", "../services/cleanup", "../services/recovery"} {
		t.Run(dir, func(t *testing.T) {
			fset, files := parsePackageDir(t, dir)
			for _, file := range files {
				for _, decl := range file.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}
					for _, spec := range gen.Specs {
						value, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range value.Names {
							if !strings.HasPrefix(name.Name, "ConfigKey") {
								continue
							}
							assert.False(t, bypassNames.MatchString(name.Name),
								"%s: configuration key constant [%s] controls verification",
								fset.Position(name.Pos()), name.Name)
							if i < len(value.Values) {
								if lit, ok := value.Values[i].(*ast.BasicLit); ok {
									assert.False(t, bypassNames.MatchString(lit.Value),
										"%s: configuration key [%s] controls verification",
										fset.Position(lit.Pos()), lit.Value)
								}
							}
						}
					}
				}
			}
		})
	}
}

// TestCheckResultsAreNotDiscarded asserts that no caller of an integrity check
// throws its verdict away. A check whose error is assigned to the blank
// identifier, or called as a bare statement, reports nothing and is
// indistinguishable at runtime from a check that was never added — which is
// exactly the bypass this file exists to prevent, arrived at by accident rather
// than by design.
func TestCheckResultsAreNotDiscarded(t *testing.T) {
	for _, dir := range checkedPackages {
		if dir == "." {
			continue // the checks do not call each other
		}
		t.Run(dir, func(t *testing.T) {
			fset, files := parsePackageDir(t, dir)
			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					switch stmt := n.(type) {
					case *ast.ExprStmt:
						// integrity.CheckX(...) as a statement of its own
						assert.False(t, isIntegrityCheckCall(stmt.X),
							"%s: the result of this integrity check is discarded",
							fset.Position(stmt.Pos()))
					case *ast.AssignStmt:
						if len(stmt.Rhs) != 1 || !isIntegrityCheckCall(stmt.Rhs[0]) {
							return true
						}
						for _, lhs := range stmt.Lhs {
							ident, ok := lhs.(*ast.Ident)
							assert.False(t, ok && ident.Name == "_",
								"%s: the result of this integrity check is assigned to the blank identifier",
								fset.Position(stmt.Pos()))
						}
					}

					return true
				})
			}
		})
	}
}

// isIntegrityCheckCall reports whether expr is a call of the form
// integrity.CheckSomething(...).
func isIntegrityCheckCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "Check") {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)

	return ok && pkg.Name == "integrity"
}
