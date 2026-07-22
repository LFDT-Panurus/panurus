/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests protos.go which provides protobuf conversion utilities.
// Tests cover ToProtosSlice and FromProtosSlice with various edge cases including nil handling.
package protos

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test types for proto conversion
type TestProto struct {
	Value string
}

type TestSource struct {
	data string
	err  error
}

func (t *TestSource) ToProtos() (*TestProto, error) {
	if t.err != nil {
		return nil, t.err
	}

	return &TestProto{Value: t.data}, nil
}

type TestDestination struct {
	data string
}

func (t *TestDestination) FromProtos(p *TestProto) error {
	if p == nil {
		return errors.New("nil proto")
	}
	t.data = p.Value

	return nil
}

// TestToProtosSlice verifies ToProtosSlice converts slices correctly
func TestToProtosSlice(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		var sources []*TestSource
		result, err := ToProtosSlice[TestProto](sources)

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("single element", func(t *testing.T) {
		sources := []*TestSource{{data: "test"}}
		result, err := ToProtosSlice[TestProto](sources)

		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "test", result[0].Value)
	})

	t.Run("multiple elements", func(t *testing.T) {
		sources := []*TestSource{
			{data: "first"},
			{data: "second"},
			{data: "third"},
		}
		result, err := ToProtosSlice[TestProto](sources)

		require.NoError(t, err)
		require.Len(t, result, 3)
		assert.Equal(t, "first", result[0].Value)
		assert.Equal(t, "second", result[1].Value)
		assert.Equal(t, "third", result[2].Value)
	})

	t.Run("with nil element", func(t *testing.T) {
		sources := []*TestSource{
			{data: "first"},
			nil,
			{data: "third"},
		}
		result, err := ToProtosSlice[TestProto](sources)

		require.NoError(t, err)
		require.Len(t, result, 3)
		assert.Equal(t, "first", result[0].Value)
		assert.Nil(t, result[1])
		assert.Equal(t, "third", result[2].Value)
	})

	t.Run("error during conversion", func(t *testing.T) {
		expectedErr := errors.New("conversion error")
		sources := []*TestSource{
			{data: "first"},
			{err: expectedErr},
		}
		result, err := ToProtosSlice[TestProto](sources)

		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
		assert.Nil(t, result)
	})
}

// TestToProtosSliceFunc verifies ToProtosSliceFunc with custom converter
func TestToProtosSliceFunc(t *testing.T) {
	converter := func(s string) (*TestProto, error) {
		if s == "" {
			return nil, errors.New("empty string")
		}

		return &TestProto{Value: s}, nil
	}

	t.Run("empty slice", func(t *testing.T) {
		var sources []string
		result, err := ToProtosSliceFunc(sources, converter)

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("successful conversion", func(t *testing.T) {
		sources := []string{"a", "b", "c"}
		result, err := ToProtosSliceFunc(sources, converter)

		require.NoError(t, err)
		require.Len(t, result, 3)
		assert.Equal(t, "a", result[0].Value)
		assert.Equal(t, "b", result[1].Value)
		assert.Equal(t, "c", result[2].Value)
	})

	t.Run("error during conversion", func(t *testing.T) {
		sources := []string{"a", "", "c"}
		result, err := ToProtosSliceFunc(sources, converter)

		require.Error(t, err)
		assert.Nil(t, result)
	})
}

// TestFromProtosSlice verifies FromProtosSlice converts back correctly
func TestFromProtosSlice(t *testing.T) {
	t.Run("successful conversion", func(t *testing.T) {
		protos := []*TestProto{
			{Value: "first"},
			{Value: "second"},
		}
		dests := []*TestDestination{{}, {}}

		err := FromProtosSlice(protos, dests)

		require.NoError(t, err)
		assert.Equal(t, "first", dests[0].data)
		assert.Equal(t, "second", dests[1].data)
	})

	t.Run("with nil proto", func(t *testing.T) {
		protos := []*TestProto{
			{Value: "first"},
			nil,
			{Value: "third"},
		}
		dests := []*TestDestination{{}, {}, {}}

		err := FromProtosSlice(protos, dests)

		require.NoError(t, err)
		assert.Equal(t, "first", dests[0].data)
		// When proto is nil, FromProtosSlice assigns a zero value S (which is nil for *TestDestination)
		assert.Nil(t, dests[1], "nil proto should result in nil destination")
		assert.Equal(t, "third", dests[2].data)
	})

	// T-GAP-C12: FromProtosSlice indexed t[i] without checking len(t) == len(s)
	// first. A deserialized proto message can legitimately carry a shorter
	// repeated field than the destination slice the caller pre-sized (e.g. a
	// crafted request.IssueMetadata with fewer Inputs than IssueInputMetadata
	// entries the caller allocated from a different, unrelated length), which
	// panicked with an index-out-of-range instead of returning an error.
	t.Run("destination slice longer than protos slice", func(t *testing.T) {
		protos := []*TestProto{{Value: "first"}}
		dests := []*TestDestination{{}, {}, {}}

		var err error
		require.NotPanics(t, func() {
			err = FromProtosSlice(protos, dests)
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mismatched slice lengths")
	})

	t.Run("protos slice longer than destination slice", func(t *testing.T) {
		protos := []*TestProto{{Value: "first"}, {Value: "second"}, {Value: "third"}}
		dests := []*TestDestination{{}}

		var err error
		require.NotPanics(t, func() {
			err = FromProtosSlice(protos, dests)
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mismatched slice lengths")
	})
}

// TestFromProtosSliceFunc verifies FromProtosSliceFunc with custom converter
func TestFromProtosSliceFunc(t *testing.T) {
	converter := func(s string) (*TestProto, error) {
		if s == "error" {
			return nil, errors.New("conversion error")
		}

		return &TestProto{Value: s}, nil
	}

	t.Run("successful conversion", func(t *testing.T) {
		sources := []string{"a", "b", "c"}
		result, err := FromProtosSliceFunc(sources, converter)

		require.NoError(t, err)
		require.Len(t, result, 3)
		assert.Equal(t, "a", result[0].Value)
		assert.Equal(t, "b", result[1].Value)
		assert.Equal(t, "c", result[2].Value)
	})

	t.Run("error during conversion", func(t *testing.T) {
		sources := []string{"a", "error", "c"}
		result, err := FromProtosSliceFunc(sources, converter)

		require.Error(t, err)
		assert.Nil(t, result)
	})
}

// TestFromProtosSliceFunc2 verifies FromProtosSliceFunc2 returns non-pointer values
func TestFromProtosSliceFunc2(t *testing.T) {
	converter := func(s string) (TestProto, error) {
		if s == "error" {
			return TestProto{}, errors.New("conversion error")
		}

		return TestProto{Value: s}, nil
	}

	t.Run("empty slice", func(t *testing.T) {
		var sources []string
		result, err := FromProtosSliceFunc2(sources, converter)

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("successful conversion", func(t *testing.T) {
		sources := []string{"x", "y", "z"}
		result, err := FromProtosSliceFunc2(sources, converter)

		require.NoError(t, err)
		require.Len(t, result, 3)
		assert.Equal(t, "x", result[0].Value)
		assert.Equal(t, "y", result[1].Value)
		assert.Equal(t, "z", result[2].Value)
	})

	t.Run("error during conversion", func(t *testing.T) {
		sources := []string{"x", "error", "z"}
		result, err := FromProtosSliceFunc2(sources, converter)

		require.Error(t, err)
		assert.Nil(t, result)
	})
}
