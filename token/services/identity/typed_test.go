/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypedIdentity_Bytes(t *testing.T) {
	ti := identity.TypedIdentity{
		Type:     identity.Type(5),
		Identity: driver.Identity("testIdentity"),
	}

	bytes, err := ti.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, bytes)
}

func TestUnmarshalTypedIdentity(t *testing.T) {
	ti := identity.TypedIdentity{
		Type:     identity.Type(5),
		Identity: driver.Identity("testIdentity"),
	}

	bytes, err := ti.Bytes()
	require.NoError(t, err)

	unmarshaledTI, err := identity.UnmarshalTypedIdentity(bytes)
	require.NoError(t, err)
	assert.Equal(t, ti, *unmarshaledTI)
}

func TestUnmarshalTypedIdentity_Error(t *testing.T) {
	invalidBytes := []byte{0xff, 0xfe, 0xfd}

	_, err := identity.UnmarshalTypedIdentity(invalidBytes)
	require.Error(t, err)
}

func TestWrapWithType(t *testing.T) {
	idType := identity.Type(5)
	id := driver.Identity("testIdentity")

	wrappedID, err := identity.WrapWithType(idType, id)
	require.NoError(t, err)
	assert.NotNil(t, wrappedID)

	unmarshaledTI, err := identity.UnmarshalTypedIdentity(wrappedID)
	require.NoError(t, err)
	assert.Equal(t, idType, unmarshaledTI.Type)
	assert.Equal(t, id, unmarshaledTI.Identity)
}

func TestTypeToString(t *testing.T) {
	// The numeric values are persisted in the owner field of tokens, they must never change.
	for _, tc := range []struct {
		idType   driver.IdentityType
		expected int32
		str      string
	}{
		{driver.IdemixIdentityType, 1, "idemix"},
		{driver.X509IdentityType, 2, "x509"},
		{driver.IdemixNymIdentityType, 3, "idemixnym"},
		{driver.HTLCScriptIdentityType, 4, "htlc"},
		{driver.MultiSigIdentityType, 5, "multisig"},
		{driver.PolicyIdentityType, 6, "policy"},
		{driver.LazerIdentityType, 7, "lazer"},
	} {
		assert.Equal(t, tc.expected, tc.idType, tc.str)
		assert.Equal(t, tc.str, identity.TypeToString(tc.idType))
	}
	assert.Equal(t, "Type (0)", identity.TypeToString(driver.ZeroIdentityType))
	assert.Equal(t, "Type (42)", identity.TypeToString(42))
}

// TestTypeToStringIsExhaustive parses the package token/driver and checks that
// TypeToString maps every *IdentityType constant, except ZeroIdentityType, to a
// distinct string: a new type cannot be forgotten.
func TestTypeToStringIsExhaustive(t *testing.T) {
	var names []string
	for _, path := range nonTestGoFiles(t, "../../driver") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			// by name, as a spec may omit the type or use int32 (IdentityType is an alias)
			for _, spec := range gd.Specs {
				for _, n := range spec.(*ast.ValueSpec).Names {
					if strings.HasSuffix(n.Name, "IdentityType") && n.Name != "ZeroIdentityType" {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	// keep in sync with the constants of token/driver: the parse above fails the
	// test when one is added here but not there, or the other way around
	values := map[string]driver.IdentityType{
		"IdemixIdentityType":     driver.IdemixIdentityType,
		"X509IdentityType":       driver.X509IdentityType,
		"IdemixNymIdentityType":  driver.IdemixNymIdentityType,
		"HTLCScriptIdentityType": driver.HTLCScriptIdentityType,
		"MultiSigIdentityType":   driver.MultiSigIdentityType,
		"PolicyIdentityType":     driver.PolicyIdentityType,
		"LazerIdentityType":      driver.LazerIdentityType,
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(values)), names, "IdentityType constants in token/driver/wallet.go")
	seen := map[string]string{}
	for _, name := range names {
		s := identity.TypeToString(values[name])
		assert.NotEqual(t, fmt.Sprintf("Type (%d)", values[name]), s, "%s has no case in TypeToString", name)
		if other, dup := seen[s]; dup {
			assert.Failf(t, "duplicate string", "%s and %s both map to %q", name, other, s)
		}
		seen[s] = name
	}
}

// nonTestGoFiles returns the non-test Go files in dir.
func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	files = slices.DeleteFunc(files, func(f string) bool { return strings.HasSuffix(f, "_test.go") })
	require.NotEmpty(t, files)

	return files
}
