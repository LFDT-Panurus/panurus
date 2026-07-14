/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package boolpolicy implements a recursive-descent parser for boolean
// expressions of the form "$0 OR ($1 AND $2)", where $N is an index
// reference into a caller-supplied slice of bool values.
//
// Grammar (OR binds less tightly than AND):
//
//	expr      = or_expr
//
//	or_expr   = and_expr ( 'OR' and_expr )*
//	and_expr  = primary  ( 'AND' primary  )*
//	primary   = '$' digits | '(' expr ')'
package boolpolicy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	// maxPolicyLen is the maximum byte length accepted by Parse.
	// Policies longer than 4 KB cannot represent a sensible real-world
	// expression and are almost certainly an attack or a bug.
	maxPolicyLen = 4 * 1024

	// maxParseDepth is the maximum parenthesis nesting depth allowed by the
	// recursive-descent parser.  Each open parenthesis adds one frame to the
	// Go call stack; capping at 64 prevents goroutine stack exhaustion from
	// attacker-supplied deeply-nested expressions.
	maxParseDepth = 64
)

// ---------------------------------------------------------------------------
// AST nodes
// ---------------------------------------------------------------------------

// Node is the common interface for all AST nodes produced by Parse.
type Node interface {
	// Eval evaluates the node against a slice of resolved boolean values.
	// A RefNode with index i returns refs[i]; out-of-range indices return false.
	Eval(refs []bool) bool
	String() string
}

// RefNode references a single boolean value by its index (e.g. $3).
type RefNode struct {
	Index int
}

func (r *RefNode) Eval(refs []bool) bool {
	if r.Index < 0 || r.Index >= len(refs) {
		return false
	}

	return refs[r.Index]
}

func (r *RefNode) String() string { return fmt.Sprintf("$%d", r.Index) }

// AndNode represents Left AND Right.
type AndNode struct {
	Left, Right Node
}

func (a *AndNode) Eval(refs []bool) bool { return a.Left.Eval(refs) && a.Right.Eval(refs) }
func (a *AndNode) String() string        { return fmt.Sprintf("(%s AND %s)", a.Left, a.Right) }

// OrNode represents Left OR Right.
type OrNode struct {
	Left, Right Node
}

func (o *OrNode) Eval(refs []bool) bool { return o.Left.Eval(refs) || o.Right.Eval(refs) }
func (o *OrNode) String() string        { return fmt.Sprintf("(%s OR %s)", o.Left, o.Right) }

// ---------------------------------------------------------------------------
// Lexer
// ---------------------------------------------------------------------------

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokRef
	tokAnd
	tokOr
	tokLParen
	tokRParen
)

type lexToken struct {
	kind  tokenKind
	index int // populated for tokRef
}

type lexer struct {
	runes []rune
	pos   int
}

func newLexer(s string) *lexer { return &lexer{runes: []rune(s)} }

func (l *lexer) skipSpace() {
	for l.pos < len(l.runes) && unicode.IsSpace(l.runes[l.pos]) {
		l.pos++
	}
}

func (l *lexer) next() (lexToken, error) {
	l.skipSpace()
	if l.pos >= len(l.runes) {
		return lexToken{kind: tokEOF}, nil
	}

	ch := l.runes[l.pos]

	switch {
	case ch == '(':
		l.pos++

		return lexToken{kind: tokLParen}, nil

	case ch == ')':
		l.pos++

		return lexToken{kind: tokRParen}, nil

	case ch == '$':
		l.pos++ // consume '$'
		start := l.pos
		for l.pos < len(l.runes) && unicode.IsDigit(l.runes[l.pos]) {
			l.pos++
		}
		if l.pos == start {
			return lexToken{}, fmt.Errorf("expected digit after '$' at position %d", l.pos)
		}
		idx, err := strconv.Atoi(string(l.runes[start:l.pos]))
		if err != nil {
			return lexToken{}, fmt.Errorf("invalid index at position %d: %w", start, err)
		}

		return lexToken{kind: tokRef, index: idx}, nil

	case unicode.IsLetter(ch):
		start := l.pos
		for l.pos < len(l.runes) && unicode.IsLetter(l.runes[l.pos]) {
			l.pos++
		}
		word := strings.ToUpper(string(l.runes[start:l.pos]))
		switch word {
		case "AND":
			return lexToken{kind: tokAnd}, nil
		case "OR":
			return lexToken{kind: tokOr}, nil
		default:
			return lexToken{}, fmt.Errorf("unknown keyword %q at position %d", word, start)
		}

	default:
		return lexToken{}, fmt.Errorf("unexpected character %q at position %d", string(ch), l.pos)
	}
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// parser holds a one-token lookahead over the lexer stream.
type parser struct {
	lex     *lexer
	current lexToken
	err     error
}

// Parse parses a boolean expression string and returns the root AST node.
// It returns an error for any lexical or syntactic problems.
//
// Parse enforces two hard limits to prevent resource exhaustion:
//   - input longer than maxPolicyLen bytes is rejected immediately.
//   - parenthesis nesting deeper than maxParseDepth levels is rejected;
//     this bounds the Go call-stack depth of the recursive descent and
//     prevents goroutine stack exhaustion from attacker-supplied input.
func Parse(input string) (Node, error) {
	if len(input) > maxPolicyLen {
		return nil, fmt.Errorf("policy expression exceeds maximum length of %d bytes (got %d)", maxPolicyLen, len(input))
	}

	p := &parser{lex: newLexer(input)}
	p.advance() // prime the lookahead
	if p.err != nil {
		return nil, p.err
	}

	node := p.parseOr(0)
	if p.err != nil {
		return nil, p.err
	}
	if p.current.kind != tokEOF {
		return nil, errors.New("unexpected token after expression")
	}

	return node, nil
}

func (p *parser) advance() {
	if p.err != nil {
		return
	}
	p.current, p.err = p.lex.next()
}

// parseOr handles: and_expr ( 'OR' and_expr )*
// OR is left-associative and lower precedence than AND.
func (p *parser) parseOr(depth int) Node {
	left := p.parseAnd(depth)
	for p.err == nil && p.current.kind == tokOr {
		p.advance()
		right := p.parseAnd(depth)
		left = &OrNode{Left: left, Right: right}
	}

	return left
}

// parseAnd handles: primary ( 'AND' primary )*
// AND is left-associative and higher precedence than OR.
func (p *parser) parseAnd(depth int) Node {
	left := p.parsePrimary(depth)
	for p.err == nil && p.current.kind == tokAnd {
		p.advance()
		right := p.parsePrimary(depth)
		left = &AndNode{Left: left, Right: right}
	}

	return left
}

// parsePrimary handles: '$' digits | '(' expr ')'
func (p *parser) parsePrimary(depth int) Node {
	if p.err != nil {
		return nil
	}
	switch p.current.kind {
	case tokRef:
		node := &RefNode{Index: p.current.index}
		p.advance()

		return node

	case tokLParen:
		if depth >= maxParseDepth {
			p.err = fmt.Errorf("policy expression exceeds maximum nesting depth of %d", maxParseDepth)

			return nil
		}
		p.advance() // consume '('
		node := p.parseOr(depth + 1)
		if p.err != nil {
			return nil
		}
		if p.current.kind != tokRParen {
			p.err = fmt.Errorf("expected ')' but got token kind %v", p.current.kind)

			return nil
		}
		p.advance() // consume ')'

		return node

	default:
		p.err = fmt.Errorf("expected '$N' or '(' at position %d", p.lex.pos)

		return nil
	}
}
