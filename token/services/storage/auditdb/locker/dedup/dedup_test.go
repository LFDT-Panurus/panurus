/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dedup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAndSort(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single element",
			input:    []string{"alice"},
			expected: []string{"alice"},
		},
		{
			name:     "already sorted and unique",
			input:    []string{"alice", "bob", "charlie"},
			expected: []string{"alice", "bob", "charlie"},
		},
		{
			name:     "unsorted is sorted ascending",
			input:    []string{"charlie", "alice", "bob"},
			expected: []string{"alice", "bob", "charlie"},
		},
		{
			name:     "duplicates removed",
			input:    []string{"bob", "alice", "bob", "alice", "bob"},
			expected: []string{"alice", "bob"},
		},
		{
			name:     "intersecting sets resolve to the same order (deadlock-free invariant)",
			input:    []string{"e2", "e1"},
			expected: []string{"e1", "e2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AndSort(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestAndSort_CanonicalOrderIsStable checks the property the lockers rely on:
// regardless of the input order, two slices with the same set of enrollment IDs
// produce the identical acquisition order, so they cannot deadlock.
func TestAndSort_CanonicalOrderIsStable(t *testing.T) {
	a := AndSort([]string{"x", "y", "z"})
	b := AndSort([]string{"z", "y", "x"})
	c := AndSort([]string{"y", "z", "x", "y"})

	assert.Equal(t, a, b)
	assert.Equal(t, a, c)
}

// TestAdded covers the reconciliation input the lockers refuse a widening on: a
// non-empty result over a non-empty held set means the caller is asking to take
// enrollment IDs on top of the ones it already holds.
func TestAdded(t *testing.T) {
	tests := []struct {
		name     string
		want     []string
		held     []string
		expected []string
	}{
		{name: "nothing held", want: []string{"alice", "bob"}, held: nil, expected: []string{"alice", "bob"}},
		{name: "same set", want: []string{"alice", "bob"}, held: []string{"alice", "bob"}, expected: []string{}},
		{name: "narrowed", want: []string{"bob"}, held: []string{"alice", "bob"}, expected: []string{}},
		{name: "widened", want: []string{"alice", "bob"}, held: []string{"alice"}, expected: []string{"bob"}},
		{name: "replaced", want: []string{"carol"}, held: []string{"alice"}, expected: []string{"carol"}},
		{name: "nothing wanted", want: nil, held: []string{"alice"}, expected: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, Added(test.want, test.held))
		})
	}
}

// TestDropped covers the other half: the locks a narrowing re-acquisition has to
// give up, which are unreachable if it does not.
func TestDropped(t *testing.T) {
	tests := []struct {
		name     string
		held     []string
		want     []string
		expected []string
	}{
		{name: "nothing held", held: nil, want: []string{"alice"}, expected: nil},
		{name: "same set", held: []string{"alice", "bob"}, want: []string{"alice", "bob"}, expected: []string{}},
		{name: "narrowed", held: []string{"alice", "bob"}, want: []string{"bob"}, expected: []string{"alice"}},
		{name: "widened", held: []string{"alice"}, want: []string{"alice", "bob"}, expected: []string{}},
		{name: "replaced", held: []string{"alice"}, want: []string{"carol"}, expected: []string{"alice"}},
		{name: "nothing wanted", held: []string{"alice", "bob"}, want: nil, expected: []string{"alice", "bob"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, Dropped(test.held, test.want))
		})
	}
}
