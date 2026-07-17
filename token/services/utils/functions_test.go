/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests functions.go which provides generic utility functions.
// Tests cover IdentityFunc (returns input unchanged) and IsNil (reflection-based nil checking).
package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIdentityFunc verifies that IdentityFunc returns a function that returns its input unchanged
func TestIdentityFunc(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		fn := IdentityFunc[string]()
		input := "test"
		result := fn(input)
		assert.Equal(t, input, result)
	})

	t.Run("int", func(t *testing.T) {
		fn := IdentityFunc[int]()
		input := 42
		result := fn(input)
		assert.Equal(t, input, result)
	})

	t.Run("struct", func(t *testing.T) {
		type TestStruct struct {
			Field string
		}
		fn := IdentityFunc[TestStruct]()
		input := TestStruct{Field: "value"}
		result := fn(input)
		assert.Equal(t, input, result)
	})

	t.Run("pointer", func(t *testing.T) {
		fn := IdentityFunc[*string]()
		input := new(string)
		*input = "test"
		result := fn(input)
		assert.Equal(t, input, result)
		assert.Same(t, input, result, "should return the same pointer")
	})
}

// TestIsNil verifies that IsNil correctly identifies nil values
func TestIsNil(t *testing.T) {
	t.Run("nil pointer", func(t *testing.T) {
		var ptr *string
		assert.True(t, IsNil(ptr))
	})

	t.Run("non-nil pointer", func(t *testing.T) {
		str := "test"
		ptr := &str
		assert.False(t, IsNil(ptr))
	})

	t.Run("nil slice", func(t *testing.T) {
		var slice []int
		assert.True(t, IsNil(slice))
	})

	t.Run("non-nil slice", func(t *testing.T) {
		slice := []int{}
		assert.False(t, IsNil(slice))
	})

	t.Run("nil map", func(t *testing.T) {
		var m map[string]int
		assert.True(t, IsNil(m))
	})

	t.Run("non-nil map", func(t *testing.T) {
		m := make(map[string]int)
		assert.False(t, IsNil(m))
	})

	t.Run("nil channel", func(t *testing.T) {
		var ch chan int
		assert.True(t, IsNil(ch))
	})

	t.Run("non-nil channel", func(t *testing.T) {
		ch := make(chan int)
		defer close(ch)
		assert.False(t, IsNil(ch))
	})

	t.Run("nil function", func(t *testing.T) {
		var fn func()
		assert.True(t, IsNil(fn))
	})

	t.Run("non-nil function", func(t *testing.T) {
		fn := func() {}
		assert.False(t, IsNil(fn))
	})

	t.Run("nil interface", func(t *testing.T) {
		var iface any
		// Note: A nil interface{} is not detected as nil by reflection
		// because it's not a pointer/slice/map/chan/func type
		assert.True(t, IsNil(iface))
	})

	t.Run("non-nil interface", func(t *testing.T) {
		var iface any = "test"
		assert.False(t, IsNil(iface))
	})

	t.Run("non-pointer types", func(t *testing.T) {
		// Non-pointer types should return false
		assert.False(t, IsNil(42))
		assert.False(t, IsNil("string"))
		assert.False(t, IsNil(true))
		assert.False(t, IsNil(struct{}{}))
	})
}
