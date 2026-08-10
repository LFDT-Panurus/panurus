/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzParseNoPanic hunts for policy strings that panic Parse instead of
// returning a Node or an error. Parse is the entry point for
// attacker-controllable policy expressions carried inside a PolicyIdentity, so
// it must never panic and must always terminate within its resource caps
// (length, nesting depth, and node count).
func FuzzParseNoPanic(f *testing.F) {
	seeds := []string{
		"",                        // empty
		"$0",                      // single ref
		"$42",                     // multi-digit ref
		"$0 AND $1",               // simple AND
		"$0 OR $1",                // simple OR
		"$0 OR ($1 AND $2)",       // nested
		"((($0)))",                // parenthesised
		"$0 OR $0 OR $0 OR $0",    // repeated refs (exercises memoisation callers)
		"$",                       // dangling dollar
		"$0 NOT $1",               // unknown keyword
		"($0 AND $1",              // unmatched open paren
		"$0 AND $1)",              // unmatched close paren
		"$0 AND",                  // missing operand
		"$0 & $1",                 // unexpected character
		"$99999999999999999999",   // index overflows int
		"(((((((((((((((((((((",   // deeply nested opens
		"$0 OR $1 OR $2 OR $3 OR", // trailing operator
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		require.NotPanics(t, func() {
			node, err := Parse(input)
			// A successful parse must yield a non-nil node; an error must yield
			// a nil node. Neither may panic regardless of input.
			if err == nil {
				require.NotNil(t, node)
			}
		})
	})
}
