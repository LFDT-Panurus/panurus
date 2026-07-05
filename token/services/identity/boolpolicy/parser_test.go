/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Parse / AST structure tests
// ---------------------------------------------------------------------------

func TestParseRef(t *testing.T) {
	node, err := Parse("$0")
	require.NoError(t, err)
	ref, ok := node.(*RefNode)
	require.True(t, ok, "expected *RefNode")
	assert.Equal(t, 0, ref.Index)
}

func TestParseRefMultiDigit(t *testing.T) {
	node, err := Parse("$42")
	require.NoError(t, err)
	ref, ok := node.(*RefNode)
	require.True(t, ok)
	assert.Equal(t, 42, ref.Index)
}

func TestParseSimpleAnd(t *testing.T) {
	node, err := Parse("$0 AND $1")
	require.NoError(t, err)
	and, ok := node.(*AndNode)
	require.True(t, ok, "expected *AndNode")
	assert.Equal(t, &RefNode{0}, and.Left)
	assert.Equal(t, &RefNode{1}, and.Right)
}

func TestParseSimpleOr(t *testing.T) {
	node, err := Parse("$0 OR $1")
	require.NoError(t, err)
	or, ok := node.(*OrNode)
	require.True(t, ok, "expected *OrNode")
	assert.Equal(t, &RefNode{0}, or.Left)
	assert.Equal(t, &RefNode{1}, or.Right)
}

func TestParseParenthesised(t *testing.T) {
	// "$0 OR ($1 AND $2)" → OrNode{ $0, AndNode{$1,$2} }
	node, err := Parse("$0 OR ($1 AND $2)")
	require.NoError(t, err)
	or, ok := node.(*OrNode)
	require.True(t, ok, "expected *OrNode at root")
	assert.Equal(t, &RefNode{0}, or.Left)

	and, ok := or.Right.(*AndNode)
	require.True(t, ok, "expected *AndNode on right of OR")
	assert.Equal(t, &RefNode{1}, and.Left)
	assert.Equal(t, &RefNode{2}, and.Right)
}

func TestPrecedenceAndBindsTighterThanOr(t *testing.T) {
	// "$0 OR $1 AND $2" must parse as "$0 OR ($1 AND $2)"
	node, err := Parse("$0 OR $1 AND $2")
	require.NoError(t, err)
	or, ok := node.(*OrNode)
	require.True(t, ok, "expected *OrNode at root")
	assert.Equal(t, &RefNode{0}, or.Left)

	and, ok := or.Right.(*AndNode)
	require.True(t, ok, "expected *AndNode on right of OR")
	assert.Equal(t, &RefNode{1}, and.Left)
	assert.Equal(t, &RefNode{2}, and.Right)
}

func TestLeftAssociativityAnd(t *testing.T) {
	// "$0 AND $1 AND $2" → AndNode{ AndNode{$0,$1}, $2 }
	node, err := Parse("$0 AND $1 AND $2")
	require.NoError(t, err)
	outer, ok := node.(*AndNode)
	require.True(t, ok)
	assert.Equal(t, &RefNode{2}, outer.Right)

	inner, ok := outer.Left.(*AndNode)
	require.True(t, ok, "expected inner *AndNode on left")
	assert.Equal(t, &RefNode{0}, inner.Left)
	assert.Equal(t, &RefNode{1}, inner.Right)
}

func TestLeftAssociativityOr(t *testing.T) {
	// "$0 OR $1 OR $2" → OrNode{ OrNode{$0,$1}, $2 }
	node, err := Parse("$0 OR $1 OR $2")
	require.NoError(t, err)
	outer, ok := node.(*OrNode)
	require.True(t, ok)
	assert.Equal(t, &RefNode{2}, outer.Right)

	inner, ok := outer.Left.(*OrNode)
	require.True(t, ok, "expected inner *OrNode on left")
	assert.Equal(t, &RefNode{0}, inner.Left)
	assert.Equal(t, &RefNode{1}, inner.Right)
}

func TestParseNestedParens(t *testing.T) {
	// "($0 AND $1) OR ($2 AND $3)"
	node, err := Parse("($0 AND $1) OR ($2 AND $3)")
	require.NoError(t, err)
	or, ok := node.(*OrNode)
	require.True(t, ok)

	left, ok := or.Left.(*AndNode)
	require.True(t, ok)
	assert.Equal(t, &RefNode{0}, left.Left)
	assert.Equal(t, &RefNode{1}, left.Right)

	right, ok := or.Right.(*AndNode)
	require.True(t, ok)
	assert.Equal(t, &RefNode{2}, right.Left)
	assert.Equal(t, &RefNode{3}, right.Right)
}

func TestParseExtraWhitespace(t *testing.T) {
	node, err := Parse("  $0   OR  ( $1  AND  $2 )  ")
	require.NoError(t, err)
	_, ok := node.(*OrNode)
	require.True(t, ok)
}

func TestParseSingleParenRef(t *testing.T) {
	node, err := Parse("($0)")
	require.NoError(t, err)
	ref, ok := node.(*RefNode)
	require.True(t, ok)
	assert.Equal(t, 0, ref.Index)
}

// ---------------------------------------------------------------------------
// Eval tests
// ---------------------------------------------------------------------------

func TestEvalRefInBounds(t *testing.T) {
	node, _ := Parse("$1")
	assert.False(t, node.Eval([]bool{true, false}))
	assert.True(t, node.Eval([]bool{false, true}))
}

func TestEvalRefOutOfBounds(t *testing.T) {
	node, _ := Parse("$5")
	assert.False(t, node.Eval([]bool{true, true, true}))
}

func TestEvalAnd(t *testing.T) {
	node, _ := Parse("$0 AND $1")
	assert.True(t, node.Eval([]bool{true, true}))
	assert.False(t, node.Eval([]bool{true, false}))
	assert.False(t, node.Eval([]bool{false, true}))
	assert.False(t, node.Eval([]bool{false, false}))
}

func TestEvalOr(t *testing.T) {
	node, _ := Parse("$0 OR $1")
	assert.True(t, node.Eval([]bool{true, true}))
	assert.True(t, node.Eval([]bool{true, false}))
	assert.True(t, node.Eval([]bool{false, true}))
	assert.False(t, node.Eval([]bool{false, false}))
}

func TestEvalOrWithParenAnd(t *testing.T) {
	// "$0 OR ($1 AND $2)"
	node, _ := Parse("$0 OR ($1 AND $2)")
	refs := func(a, b, c bool) []bool { return []bool{a, b, c} }

	assert.True(t, node.Eval(refs(true, false, false)))  // $0 alone is enough
	assert.True(t, node.Eval(refs(false, true, true)))   // $1 AND $2
	assert.False(t, node.Eval(refs(false, true, false))) // $1 AND $2 fails
	assert.False(t, node.Eval(refs(false, false, false)))
}

func TestEvalPrecedence(t *testing.T) {
	// "$0 OR $1 AND $2" == "$0 OR ($1 AND $2)"
	node, _ := Parse("$0 OR $1 AND $2")
	assert.True(t, node.Eval([]bool{true, false, false}))
	assert.True(t, node.Eval([]bool{false, true, true}))
	assert.False(t, node.Eval([]bool{false, true, false}))
}

func TestEvalComplexNested(t *testing.T) {
	// "($0 AND $1) OR ($2 AND $3)"
	node, _ := Parse("($0 AND $1) OR ($2 AND $3)")
	assert.True(t, node.Eval([]bool{true, true, false, false}))
	assert.True(t, node.Eval([]bool{false, false, true, true}))
	assert.True(t, node.Eval([]bool{true, true, true, true}))
	assert.False(t, node.Eval([]bool{true, false, false, true}))
}

// ---------------------------------------------------------------------------
// String() round-trip tests
// ---------------------------------------------------------------------------

func TestStringRef(t *testing.T) {
	assert.Equal(t, "$7", (&RefNode{7}).String())
}

func TestStringAnd(t *testing.T) {
	node, _ := Parse("$0 AND $1")
	assert.Equal(t, "($0 AND $1)", node.String())
}

func TestStringOr(t *testing.T) {
	node, _ := Parse("$0 OR $1")
	assert.Equal(t, "($0 OR $1)", node.String())
}

func TestStringNested(t *testing.T) {
	node, _ := Parse("$0 OR ($1 AND $2)")
	assert.Equal(t, "($0 OR ($1 AND $2))", node.String())
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestErrorEmptyInput(t *testing.T) {
	_, err := Parse("")
	assert.Error(t, err)
}

func TestErrorDollarNoDigit(t *testing.T) {
	_, err := Parse("$")
	assert.Error(t, err)
}

func TestErrorUnknownKeyword(t *testing.T) {
	_, err := Parse("$0 NOT $1")
	assert.Error(t, err)
}

func TestErrorUnmatchedOpenParen(t *testing.T) {
	_, err := Parse("($0 AND $1")
	assert.Error(t, err)
}

func TestErrorUnmatchedCloseParen(t *testing.T) {
	_, err := Parse("$0 AND $1)")
	assert.Error(t, err)
}

func TestErrorMissingOperand(t *testing.T) {
	_, err := Parse("$0 AND")
	assert.Error(t, err)
}

func TestErrorUnexpectedCharacter(t *testing.T) {
	_, err := Parse("$0 & $1")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Security: resource-exhaustion guards
// ---------------------------------------------------------------------------

func TestErrorInputTooLong(t *testing.T) {
	// Build a string that is exactly one byte over the limit.
	// Its content is irrelevant; we just need length > maxPolicyLen.
	long := make([]byte, maxPolicyLen+1)
	for i := range long {
		long[i] = 'x'
	}
	_, err := Parse(string(long))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestErrorNestingTooDeep(t *testing.T) {
	// Build a policy with maxParseDepth+1 open parentheses, e.g.
	// "(((... $0 ...)))" — one level deeper than the allowed limit.
	depth := maxParseDepth + 1
	policy := ""
	var policySb285 strings.Builder
	for range depth {
		policySb285.WriteString("(")
	}
	policy += policySb285.String()
	policy += "$0"
	var policySb289 strings.Builder
	for range depth {
		policySb289.WriteString(")")
	}
	policy += policySb289.String()
	_, err := Parse(policy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum nesting depth")
}

func TestNestingAtLimitIsAllowed(t *testing.T) {
	// A policy nested exactly at maxParseDepth must succeed.
	policy := ""
	var policySb300 strings.Builder
	for range maxParseDepth {
		policySb300.WriteString("(")
	}
	policy += policySb300.String()
	policy += "$0"
	var policySb304 strings.Builder
	for range maxParseDepth {
		policySb304.WriteString(")")
	}
	policy += policySb304.String()
	_, err := Parse(policy)
	require.NoError(t, err)
}
