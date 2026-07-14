/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToQuantity(t *testing.T) {
	_, err := token.ToQuantity(ToHex(100), 0)
	require.EqualError(t, err, "precision must be larger than 0")

	_, err = token.ToQuantity(IntToHex(-100), 64)
	require.EqualError(t, err, "invalid input [0x-64,64]")

	_, err = token.ToQuantity("abc", 64)
	require.EqualError(t, err, "invalid input [abc,64]")

	_, err = token.ToQuantity("0babc", 64)
	require.EqualError(t, err, "invalid input [0babc,64]")

	_, err = token.ToQuantity("0abc", 64)
	require.EqualError(t, err, "invalid input [0abc,64]")

	_, err = token.ToQuantity("0xabc", 2)
	require.EqualError(t, err, "0xabc has precision 12 > 2")

	_, err = token.ToQuantity("10231", 64)
	require.NoError(t, err)

	_, err = token.ToQuantity("0xABC", 64)
	require.NoError(t, err)

	_, err = token.ToQuantity("0XABC", 64)
	require.NoError(t, err)

	_, err = token.ToQuantity("0XAbC", 64)
	require.NoError(t, err)

	_, err = token.ToQuantity("0xAbC", 64)
	require.NoError(t, err)

	_, err = token.ToQuantity(IntToHex(-100), 128)
	require.EqualError(t, err, "invalid input [0x-64,128]")

	_, err = token.ToQuantity("abc", 128)
	require.EqualError(t, err, "invalid input [abc,128]")

	_, err = token.ToQuantity("0babc", 128)
	require.EqualError(t, err, "invalid input [0babc,128]")

	_, err = token.ToQuantity("0abc", 128)
	require.EqualError(t, err, "invalid input [0abc,128]")

	_, err = token.ToQuantity("0xabc", 2)
	require.EqualError(t, err, "0xabc has precision 12 > 2")

	_, err = token.ToQuantity("10231", 128)
	require.NoError(t, err)

	_, err = token.ToQuantity("0xABC", 128)
	require.NoError(t, err)

	_, err = token.ToQuantity("0XABC", 128)
	require.NoError(t, err)

	_, err = token.ToQuantity("0XAbC", 128)
	require.NoError(t, err)

	_, err = token.ToQuantity("0xAbC", 128)
	require.NoError(t, err)
}

func TestDecimal(t *testing.T) {
	q, err := token.ToQuantity("10231", 64)
	require.NoError(t, err)
	assert.Equal(t, "10231", q.Decimal())

	q, err = token.ToQuantity("10231", 128)
	require.NoError(t, err)
	assert.Equal(t, "10231", q.Decimal())
}

func TestHex(t *testing.T) {
	q, err := token.ToQuantity("0xabc", 64)
	require.NoError(t, err)
	assert.Equal(t, "0xabc", q.Hex())

	q, err = token.ToQuantity("0xabc", 128)
	require.NoError(t, err)
	assert.Equal(t, "0xabc", q.Hex())
}

func TestOverflow(t *testing.T) {
	half := uint64(math.MaxUint64 / 2)
	assert.Equal(t, uint64(math.MaxUint64), half+half+1)

	a, err := token.ToQuantity(ToHex(1), 64)
	require.NoError(t, err)
	b, err := token.ToQuantity(ToHex(uint64(math.MaxUint64)), 64)
	require.NoError(t, err)

	_, err = a.Add(b)
	require.Error(t, err)
	_, err = b.Add(b)
	require.Error(t, err)

	abig, err := token.NewUBigQuantity(ToHex(1), 64)
	require.NoError(t, err)
	bbig, err := token.NewUBigQuantity(ToHex(uint64(math.MaxUint64)), 64)
	require.NoError(t, err)

	_, err = abig.Add(bbig)
	require.Error(t, err)
	_, err = bbig.Add(bbig)
	assert.Error(t, err)
}

func TestBigQuantity_Add(t *testing.T) {
	q := token.NewZeroQuantity(128)
	b := token.NewOneQuantity(128)

	result, err := q.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
}

func TestBigQuantity_Sub(t *testing.T) {
	q := token.NewOneQuantity(128)
	b := token.NewOneQuantity(128)

	result, err := q.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, "0", result.Decimal())
}

func TestBigQuantity_Cmp(t *testing.T) {
	q := token.NewZeroQuantity(128).(*token.BigQuantity)
	b := token.NewOneQuantity(128)

	assert.Equal(t, -1, q.Cmp(b))

	q = token.NewOneQuantity(128).(*token.BigQuantity)
	assert.Equal(t, 0, q.Cmp(b))

	q = token.NewOneQuantity(128).(*token.BigQuantity)
	b = token.NewZeroQuantity(128)
	assert.Equal(t, 1, q.Cmp(b))
}

func TestBigQuantity_Hex(t *testing.T) {
	q := token.NewZeroQuantity(128).(*token.BigQuantity)

	assert.Equal(t, "0x0", q.Hex())
}

func TestBigQuantity_Decimal(t *testing.T) {
	q := token.NewZeroQuantity(128).(*token.BigQuantity)

	assert.Equal(t, "0", q.Decimal())
}

func TestUInt64Quantity_Add(t *testing.T) {
	q := token.NewOneQuantity(64)
	b := token.NewZeroQuantity(64)

	result, err := q.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
}

func TestUInt64Quantity_Sub(t *testing.T) {
	q := token.NewQuantityFromUInt64(2)
	b := token.NewOneQuantity(64)

	result, err := q.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
}

func TestUInt64Quantity_Cmp(t *testing.T) {
	q := token.NewOneQuantity(64).(*token.UInt64Quantity)
	b := token.NewQuantityFromUInt64(2)

	assert.Equal(t, -1, q.Cmp(b))

	q = token.NewQuantityFromUInt64(2).(*token.UInt64Quantity)
	assert.Equal(t, 0, q.Cmp(b))

	q = token.NewQuantityFromUInt64(2).(*token.UInt64Quantity)
	b = token.NewOneQuantity(64)
	assert.Equal(t, 1, q.Cmp(b))
}

func TestUInt64Quantity_Hex(t *testing.T) {
	q := token.NewQuantityFromUInt64(255).(*token.UInt64Quantity)

	assert.Equal(t, "0xff", q.Hex())
}

func TestUInt64Quantity_Decimal(t *testing.T) {
	q := token.NewQuantityFromUInt64(123).(*token.UInt64Quantity)

	assert.Equal(t, "123", q.Decimal())
}

func TestCrossTypeComparisons(t *testing.T) {
	// Test UInt64Quantity vs BigQuantity
	u64One := token.NewOneQuantity(64).(*token.UInt64Quantity)
	bgOne := token.NewOneQuantity(128)
	assert.Equal(t, 0, u64One.Cmp(bgOne))

	u64Two := token.NewQuantityFromUInt64(2).(*token.UInt64Quantity)
	assert.Equal(t, 1, u64Two.Cmp(bgOne))

	u64Zero := token.NewZeroQuantity(64).(*token.UInt64Quantity)
	assert.Equal(t, -1, u64Zero.Cmp(bgOne)) // 0 < 1, so -1

	// Test BigQuantity vs UInt64Quantity
	bgOnePtr := token.NewOneQuantity(128).(*token.BigQuantity)
	u64TwoPtr := token.NewQuantityFromUInt64(2)
	assert.Equal(t, -1, bgOnePtr.Cmp(u64TwoPtr)) // 1 < 2, so -1

	u64OnePtr := token.NewQuantityFromUInt64(1)
	assert.Equal(t, 0, bgOnePtr.Cmp(u64OnePtr)) // 1 == 1, so 0

	u64ZeroPtr := token.NewZeroQuantity(64)
	assert.Equal(t, 1, bgOnePtr.Cmp(u64ZeroPtr)) // 1 > 0

	// Test equality cases
	u64Eq := token.NewQuantityFromUInt64(100).(*token.UInt64Quantity)
	bg, err := token.NewUBigQuantity("100", 128)
	require.NoError(t, err)
	assert.Equal(t, 0, u64Eq.Cmp(bg))
	assert.Equal(t, 0, bg.Cmp(u64Eq))
}

func TestBigQuantity_Add_TypeMismatch(t *testing.T) {
	q := token.NewZeroQuantity(2).(*token.BigQuantity) // Precision set to 2 bits
	b := token.NewOneQuantity(64)                      // UInt64Quantity

	_, err := q.Add(b)
	assert.Error(t, err)
}

func TestBigQuantity_Sub_Underflow(t *testing.T) {
	q := token.NewZeroQuantity(128)
	b := token.NewOneQuantity(128)

	_, err := q.Sub(b)
	assert.Error(t, err)
}

func TestUInt64Quantity_Add_Overflow(t *testing.T) {
	q := token.NewQuantityFromUInt64(18446744073709551615) // Max uint64 value
	b := token.NewQuantityFromUInt64(1)

	_, err := q.Add(b)
	assert.Error(t, err)
}

func TestUInt64Quantity_Sub_Underflow(t *testing.T) {
	q := token.NewQuantityFromUInt64(0)
	b := token.NewQuantityFromUInt64(1)

	_, err := q.Sub(b)
	assert.Error(t, err)
}

func TestBigQuantity_Add_Immutability(t *testing.T) {
	q := token.NewZeroQuantity(128)
	b := token.NewOneQuantity(128)

	result, err := q.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
	assert.Equal(t, "0", q.Decimal())
}

func TestBigQuantity_Sub_Immutability(t *testing.T) {
	q, err := token.ToQuantity("10", 128)
	require.NoError(t, err)
	b := token.NewOneQuantity(128)

	result, err := q.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, "9", result.Decimal())
	assert.Equal(t, "10", q.Decimal())
}

func TestUInt64Quantity_Add_Immutability(t *testing.T) {
	q := token.NewQuantityFromUInt64(5)
	b := token.NewQuantityFromUInt64(3)

	result, err := q.Add(b)
	require.NoError(t, err)
	assert.Equal(t, "8", result.Decimal())
	assert.Equal(t, "5", q.Decimal())
}

func TestUInt64Quantity_Sub_Immutability(t *testing.T) {
	q := token.NewQuantityFromUInt64(5)
	b := token.NewQuantityFromUInt64(3)

	result, err := q.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, "2", result.Decimal())
	assert.Equal(t, "5", q.Decimal())
}

func TestNewUBigQuantity_ValidInput(t *testing.T) {
	q, err := token.NewUBigQuantity("123456789", 64)

	require.NoError(t, err)
	assert.NotNil(t, q)
	assert.Equal(t, "123456789", q.Decimal())
}

func TestNewUBigQuantity_InvalidInput(t *testing.T) {
	_, err := token.NewUBigQuantity("invalid", 64)

	require.Error(t, err)
}

func TestNewUBigQuantity_ZeroPrecision(t *testing.T) {
	_, err := token.NewUBigQuantity("123456789", 0)

	require.Error(t, err)
}

func TestNewUBigQuantity_NegativeQuantity(t *testing.T) {
	_, err := token.NewUBigQuantity("-123", 64)

	require.Error(t, err)
}

func TestNewUBigQuantity_Overflow(t *testing.T) {
	_, err := token.NewUBigQuantity("18446744073709551616", 64) // Max uint64 + 1

	require.Error(t, err)
}

func TestUInt64ToQuantity_ValidInput(t *testing.T) {
	q, err := token.UInt64ToQuantity(123456789, 64)

	require.NoError(t, err)
	assert.NotNil(t, q)
	assert.Equal(t, "123456789", q.Decimal())
}

func TestUInt64ToQuantity_ZeroPrecision(t *testing.T) {
	_, err := token.UInt64ToQuantity(123456789, 0)

	require.Error(t, err)
}

func TestUInt64ToQuantity_NegativeQuantity(t *testing.T) {
	_, err := token.UInt64ToQuantity(18446744073709551615, 64) // Max uint64

	require.NoError(t, err)
}

func TestUInt64ToQuantity_Precision_InsufficientError(t *testing.T) {
	_, err := token.UInt64ToQuantity(18446744073709551615, 10) // Max uint64

	require.Error(t, err)
}

func TestBigQuantity_Clone(t *testing.T) {
	original, err := token.ToQuantity("100", 128)
	require.NoError(t, err)

	clone := original.Clone()
	assert.Equal(t, "100", clone.Decimal())

	originalBig := original.(*token.BigQuantity)
	cloneBig := clone.(*token.BigQuantity)
	assert.NotSame(t, originalBig.Int, cloneBig.Int)

	originalBig.SetInt64(999)
	assert.Equal(t, "100", clone.Decimal())
}

func TestUInt64Quantity_Clone(t *testing.T) {
	original, err := token.ToQuantity("100", 64)
	require.NoError(t, err)

	clone := original.Clone()
	assert.Equal(t, "100", clone.Decimal())

	_, err = original.Add(token.NewOneQuantity(64))
	require.NoError(t, err)
	assert.Equal(t, "100", clone.Decimal())
}

// Package-level sinks prevent the compiler from optimizing away the
// benchmarked, side-effect-free calls (dead-code elimination).
var (
	benchResult token.Quantity
	benchErr    error
	benchCmp    int
)

func BenchmarkBigQuantity_Add(b *testing.B) {
	q := token.NewZeroQuantity(128)
	one := token.NewOneQuantity(128)
	b.ResetTimer()
	for b.Loop() {
		benchResult, benchErr = q.Add(one)
	}
	require.NoError(b, benchErr)
}

func BenchmarkBigQuantity_Sub(b *testing.B) {
	q, err := token.ToQuantity("1000000000", 128)
	require.NoError(b, err)
	one := token.NewOneQuantity(128)
	b.ResetTimer()
	for b.Loop() {
		benchResult, benchErr = q.Sub(one)
	}
	require.NoError(b, benchErr)
}

func BenchmarkBigQuantity_Cmp(b *testing.B) {
	q := token.NewZeroQuantity(128)
	one := token.NewOneQuantity(128)
	b.ResetTimer()
	for b.Loop() {
		benchCmp = q.Cmp(one)
	}
}

func BenchmarkUInt64Quantity_Add(b *testing.B) {
	q := token.NewZeroQuantity(64)
	one := token.NewOneQuantity(64)
	b.ResetTimer()
	for b.Loop() {
		benchResult, benchErr = q.Add(one)
	}
	require.NoError(b, benchErr)
}

func BenchmarkUInt64Quantity_Sub(b *testing.B) {
	q, err := token.ToQuantity("1000000000", 64)
	require.NoError(b, err)
	one := token.NewOneQuantity(64)
	b.ResetTimer()
	for b.Loop() {
		benchResult, benchErr = q.Sub(one)
	}
	require.NoError(b, benchErr)
}

func BenchmarkUInt64Quantity_Cmp(b *testing.B) {
	q := token.NewZeroQuantity(64)
	one := token.NewOneQuantity(64)
	b.ResetTimer()
	for b.Loop() {
		benchCmp = q.Cmp(one)
	}
}

func BenchmarkToQuantity(b *testing.B) {
	for b.Loop() {
		benchResult, benchErr = token.ToQuantity("0x1234567890abcdef", 64)
	}
	require.NoError(b, benchErr)
}

func ToHex(q uint64) string {
	return "0x" + strconv.FormatUint(q, 16)
}

func IntToHex(q int64) string {
	return "0x" + strconv.FormatInt(q, 16)
}

// Additional comprehensive tests for improved coverage

func TestTypeMismatchErrors(t *testing.T) {
	t.Run("BigQuantity Add with UInt64Quantity", func(t *testing.T) {
		q1 := token.NewZeroQuantity(128).(*token.BigQuantity)
		q2 := token.NewZeroQuantity(64)

		_, err := q1.Add(q2)
		require.Error(t, err)
	})

	t.Run("BigQuantity Sub with UInt64Quantity", func(t *testing.T) {
		q1 := token.NewOneQuantity(128).(*token.BigQuantity)
		q2 := token.NewOneQuantity(64)

		_, err := q1.Sub(q2)
		require.Error(t, err)
	})

	t.Run("UInt64Quantity Add with BigQuantity", func(t *testing.T) {
		q1 := token.NewZeroQuantity(64).(*token.UInt64Quantity)
		q2 := token.NewZeroQuantity(128)

		_, err := q1.Add(q2)
		require.Error(t, err)
	})

	t.Run("UInt64Quantity Sub with BigQuantity", func(t *testing.T) {
		q1 := token.NewOneQuantity(64).(*token.UInt64Quantity)
		q2 := token.NewOneQuantity(128)

		_, err := q1.Sub(q2)
		require.Error(t, err)
	})
}

func TestBigQuantityString(t *testing.T) {
	q, err := token.NewUBigQuantity("12345", 128)
	require.NoError(t, err)

	assert.Equal(t, "12345", q.String())
}

func TestBigQuantityToBigInt(t *testing.T) {
	q, err := token.NewUBigQuantity("12345", 128)
	require.NoError(t, err)

	bigInt := q.ToBigInt()
	assert.Equal(t, "12345", bigInt.Text(10))

	// Verify it's a copy, not the same instance
	bigInt.Add(bigInt, bigInt)
	assert.Equal(t, "12345", q.Decimal()) // Original unchanged
}

func TestUInt64QuantityToBigInt(t *testing.T) {
	q := token.NewQuantityFromUInt64(12345).(*token.UInt64Quantity)

	bigInt := q.ToBigInt()
	assert.Equal(t, "12345", bigInt.Text(10))

	// Verify it's a new instance
	bigInt.Add(bigInt, bigInt)
	assert.Equal(t, "12345", q.Decimal()) // Original unchanged
}

func TestNewZeroQuantityPrecisions(t *testing.T) {
	tests := []struct {
		name      string
		precision uint64
		wantType  string
	}{
		{"64-bit precision", 64, "*token.UInt64Quantity"},
		{"128-bit precision", 128, "*token.BigQuantity"},
		{"256-bit precision", 256, "*token.BigQuantity"},
		{"32-bit precision", 32, "*token.BigQuantity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := token.NewZeroQuantity(tt.precision)
			assert.Equal(t, "0", q.Decimal())
			assert.Equal(t, tt.wantType, fmt.Sprintf("%T", q))
		})
	}
}

func TestNewOneQuantityPrecisions(t *testing.T) {
	tests := []struct {
		name      string
		precision uint64
		wantType  string
	}{
		{"64-bit precision", 64, "*token.UInt64Quantity"},
		{"128-bit precision", 128, "*token.BigQuantity"},
		{"256-bit precision", 256, "*token.BigQuantity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := token.NewOneQuantity(tt.precision)
			assert.Equal(t, "1", q.Decimal())
			assert.Equal(t, tt.wantType, fmt.Sprintf("%T", q))
		})
	}
}

func TestToQuantityEdgeCases(t *testing.T) {
	t.Run("Zero value", func(t *testing.T) {
		q, err := token.ToQuantity("0", 64)
		require.NoError(t, err)
		assert.Equal(t, "0", q.Decimal())
	})

	t.Run("Max uint64 for 64-bit precision", func(t *testing.T) {
		q, err := token.ToQuantity(ToHex(math.MaxUint64), 64)
		require.NoError(t, err)
		assert.Equal(t, strconv.FormatUint(math.MaxUint64, 10), q.Decimal())
	})

	t.Run("Large value for 128-bit precision", func(t *testing.T) {
		largeVal := "340282366920938463463374607431768211455" // 2^128 - 1
		q, err := token.ToQuantity(largeVal, 128)
		require.NoError(t, err)
		assert.Equal(t, largeVal, q.Decimal())
	})

	t.Run("Binary prefix", func(t *testing.T) {
		q, err := token.ToQuantity("0b1010", 64)
		require.NoError(t, err)
		assert.Equal(t, "10", q.Decimal())
	})

	t.Run("Octal prefix", func(t *testing.T) {
		q, err := token.ToQuantity("0o77", 64)
		require.NoError(t, err)
		assert.Equal(t, "63", q.Decimal())
	})
}

func TestUInt64ToQuantityEdgeCases(t *testing.T) {
	t.Run("Zero value", func(t *testing.T) {
		q, err := token.UInt64ToQuantity(0, 64)
		require.NoError(t, err)
		assert.Equal(t, "0", q.Decimal())
	})

	t.Run("Max uint64", func(t *testing.T) {
		q, err := token.UInt64ToQuantity(math.MaxUint64, 64)
		require.NoError(t, err)
		assert.Equal(t, strconv.FormatUint(math.MaxUint64, 10), q.Decimal())
	})

	t.Run("Small value with large precision", func(t *testing.T) {
		q, err := token.UInt64ToQuantity(100, 256)
		require.NoError(t, err)
		assert.Equal(t, "100", q.Decimal())
	})

	t.Run("Value exceeds precision", func(t *testing.T) {
		_, err := token.UInt64ToQuantity(256, 8) // 256 needs 9 bits
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has precision")
	})
}

func TestBigQuantityOperationsChaining(t *testing.T) {
	q := token.NewZeroQuantity(128)
	one := token.NewOneQuantity(128)

	// Chain operations: 0 + 1 + 1 + 1 = 3
	result, err := q.Add(one)
	require.NoError(t, err)
	result, err = result.Add(token.NewOneQuantity(128))
	require.NoError(t, err)
	result, err = result.Add(token.NewOneQuantity(128))
	require.NoError(t, err)
	assert.Equal(t, "3", result.Decimal())

	// Chain subtraction: 3 - 1 - 1 = 1
	result, err = result.Sub(token.NewOneQuantity(128))
	require.NoError(t, err)
	result, err = result.Sub(token.NewOneQuantity(128))
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
}

func TestUInt64QuantityOperationsChaining(t *testing.T) {
	q := token.NewZeroQuantity(64)
	one := token.NewOneQuantity(64)

	// Chain operations: 0 + 1 + 1 + 1 = 3
	result, err := q.Add(one)
	require.NoError(t, err)
	result, err = result.Add(token.NewOneQuantity(64))
	require.NoError(t, err)
	result, err = result.Add(token.NewOneQuantity(64))
	require.NoError(t, err)
	assert.Equal(t, "3", result.Decimal())

	// Chain subtraction: 3 - 1 - 1 = 1
	result, err = result.Sub(token.NewOneQuantity(64))
	require.NoError(t, err)
	result, err = result.Sub(token.NewOneQuantity(64))
	require.NoError(t, err)
	assert.Equal(t, "1", result.Decimal())
}

func TestCmpWithZeroValues(t *testing.T) {
	t.Run("BigQuantity zero comparisons", func(t *testing.T) {
		zero := token.NewZeroQuantity(128).(*token.BigQuantity)
		one := token.NewOneQuantity(128)

		assert.Equal(t, 0, zero.Cmp(token.NewZeroQuantity(128)))
		assert.Equal(t, -1, zero.Cmp(one))
		assert.Equal(t, 1, one.Cmp(zero))
	})

	t.Run("UInt64Quantity zero comparisons", func(t *testing.T) {
		zero := token.NewZeroQuantity(64).(*token.UInt64Quantity)
		one := token.NewOneQuantity(64)

		assert.Equal(t, 0, zero.Cmp(token.NewZeroQuantity(64)))
		assert.Equal(t, -1, zero.Cmp(one))
		assert.Equal(t, 1, one.Cmp(zero))
	})
}

func TestCmpCrossTypeWithLargeValues(t *testing.T) {
	t.Run("BigQuantity larger than uint64 max", func(t *testing.T) {
		// Create a BigQuantity larger than uint64 max
		large, err := token.NewUBigQuantity("18446744073709551616", 128) // uint64 max + 1
		require.NoError(t, err)

		u64Max := token.NewQuantityFromUInt64(math.MaxUint64)

		assert.Equal(t, 1, large.Cmp(u64Max))
		assert.Equal(t, -1, u64Max.Cmp(large))
	})

	t.Run("BigQuantity zero vs UInt64Quantity non-zero", func(t *testing.T) {
		bgZero := token.NewZeroQuantity(128).(*token.BigQuantity)
		u64One := token.NewQuantityFromUInt64(1)

		assert.Equal(t, -1, bgZero.Cmp(u64One))
		assert.Equal(t, 1, u64One.Cmp(bgZero))
	})
}

func TestHexFormatting(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"Small value", "15", "0xf"},
		{"Zero", "0", "0x0"},
		{"Large value", "255", "0xff"},
		{"Power of 2", "256", "0x100"},
	}

	for _, tt := range tests {
		t.Run(tt.name+" BigQuantity", func(t *testing.T) {
			q, err := token.NewUBigQuantity(tt.value, 128)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, q.Hex())
		})

		t.Run(tt.name+" UInt64Quantity", func(t *testing.T) {
			val, _ := strconv.ParseUint(tt.value, 10, 64)
			q := token.NewQuantityFromUInt64(val).(*token.UInt64Quantity)
			assert.Equal(t, tt.expected, q.Hex())
		})
	}
}

func TestNewQuantityFromUInt64(t *testing.T) {
	tests := []uint64{0, 1, 100, 1000, math.MaxUint64}

	for _, val := range tests {
		t.Run(fmt.Sprintf("Value %d", val), func(t *testing.T) {
			q := token.NewQuantityFromUInt64(val)
			assert.Equal(t, strconv.FormatUint(val, 10), q.Decimal())
			assert.IsType(t, &token.UInt64Quantity{}, q)
		})
	}
}

func TestBigQuantityPrecisionBoundary(t *testing.T) {
	t.Run("Value at exact precision boundary", func(t *testing.T) {
		// 2^7 = 128, which needs exactly 8 bits
		q, err := token.NewUBigQuantity("128", 8)
		require.NoError(t, err)
		assert.Equal(t, "128", q.Decimal())
	})

	t.Run("Value exceeds precision by 1 bit", func(t *testing.T) {
		// 2^8 = 256, which needs 9 bits
		_, err := token.NewUBigQuantity("256", 8)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has precision 9 > 8")
	})
}

func TestAddSubImmutability(t *testing.T) {
	t.Run("BigQuantity Add does not mutate receiver", func(t *testing.T) {
		q := token.NewZeroQuantity(128)

		result, err := q.Add(token.NewOneQuantity(128))
		require.NoError(t, err)

		assert.Equal(t, "1", result.Decimal())
		assert.Equal(t, "0", q.Decimal()) // Original unchanged
	})

	t.Run("UInt64Quantity Add does not mutate receiver", func(t *testing.T) {
		q := token.NewZeroQuantity(64)

		result, err := q.Add(token.NewOneQuantity(64))
		require.NoError(t, err)

		assert.Equal(t, "1", result.Decimal())
		assert.Equal(t, "0", q.Decimal()) // Original unchanged
	})
}

func TestValidateBigIntForQuantity(t *testing.T) {
	// This is tested indirectly through ToQuantity, NewUBigQuantity
	// but we can add edge cases

	t.Run("Negative values rejected", func(t *testing.T) {
		_, err := token.ToQuantity("-1", 64)
		require.Error(t, err)
		// The error can be either "invalid input" (parsing fails) or "quantity must be larger than 0"
		assert.True(t,
			err.Error() == "invalid input [-1,64]" ||
				err.Error() == "quantity must be larger than 0",
			"unexpected error: %v", err)
	})

	t.Run("Very large precision", func(t *testing.T) {
		q, err := token.ToQuantity("1", 1024)
		require.NoError(t, err)
		assert.Equal(t, "1", q.Decimal())
	})
}

// InvalidQuantity is a mock type for testing panic conditions
type InvalidQuantity struct{}

func (InvalidQuantity) Add(b token.Quantity) (token.Quantity, error) { return nil, nil }
func (InvalidQuantity) Sub(b token.Quantity) (token.Quantity, error) { return nil, nil }
func (InvalidQuantity) Cmp(b token.Quantity) int                     { return 0 }
func (InvalidQuantity) Hex() string                                  { return "" }
func (InvalidQuantity) Decimal() string                              { return "" }
func (InvalidQuantity) ToBigInt() *big.Int                           { return nil }
func (InvalidQuantity) Clone() token.Quantity                        { return InvalidQuantity{} }

func TestCmpPanicOnInvalidType(t *testing.T) {
	t.Run("BigQuantity Cmp with invalid type", func(t *testing.T) {
		q := token.NewZeroQuantity(128).(*token.BigQuantity)
		invalid := InvalidQuantity{}

		assert.Panics(t, func() {
			q.Cmp(invalid)
		})
	})

	t.Run("UInt64Quantity Cmp with invalid type", func(t *testing.T) {
		q := token.NewZeroQuantity(64).(*token.UInt64Quantity)
		invalid := InvalidQuantity{}

		assert.Panics(t, func() {
			q.Cmp(invalid)
		})
	})
}

func TestBigQuantity_ToBigInt(t *testing.T) {
	q, err := token.NewUBigQuantity("42", 128)
	require.NoError(t, err)

	bi := q.ToBigInt()
	assert.Equal(t, int64(42), bi.Int64())

	// Verify copy semantics: mutating the result does not affect the original
	bi.SetInt64(999)
	assert.Equal(t, "42", q.Decimal())
}

func TestUInt64Quantity_ToBigInt(t *testing.T) {
	q := token.NewQuantityFromUInt64(12345)

	bi := q.ToBigInt()
	assert.Equal(t, int64(12345), bi.Int64())
}

func TestQuantity_TypeMismatchReturnsError(t *testing.T) {
	u64, err := token.UInt64ToQuantity(10, 64)
	require.NoError(t, err)

	big128, err := token.NewUBigQuantity("10", 128)
	require.NoError(t, err)

	_, err = big128.Add(u64)
	require.Error(t, err)

	_, err = big128.Sub(u64)
	require.Error(t, err)

	_, err = u64.(*token.UInt64Quantity).Add(big128)
	require.Error(t, err)

	_, err = u64.(*token.UInt64Quantity).Sub(big128)
	require.Error(t, err)
}

func TestUInt64ToQuantity_BigQuantityPath(t *testing.T) {
	// Non-64 precision should return a BigQuantity
	q, err := token.UInt64ToQuantity(100, 128)
	require.NoError(t, err)

	_, ok := q.(*token.BigQuantity)
	assert.True(t, ok, "expected BigQuantity for precision 128")
	assert.Equal(t, "100", q.Decimal())
}

func TestToQuantity_NegativeDecimal(t *testing.T) {
	_, err := token.ToQuantity("-100", 64)
	require.EqualError(t, err, "quantity must be larger than 0")

	_, err = token.ToQuantity("-1", 128)
	require.EqualError(t, err, "quantity must be larger than 0")
}

func TestToQuantitySum(t *testing.T) {
	t.Run("Valid sum", func(t *testing.T) {
		reducer := token.ToQuantitySum(64)
		s := reducer.Produce()
		var err error

		s, err = reducer.Reduce(s, &token.UnspentToken{Quantity: "10"})
		require.NoError(t, err)

		s, err = reducer.Reduce(s, &token.UnspentToken{Quantity: "20"})
		require.NoError(t, err)

		assert.Equal(t, "30", s.Decimal())
	})

	t.Run("Invalid parsing", func(t *testing.T) {
		reducer := token.ToQuantitySum(64)
		s := reducer.Produce()
		_, err := reducer.Reduce(s, &token.UnspentToken{Quantity: "invalid"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid input")
	})

	t.Run("Panic recovery", func(t *testing.T) {
		reducer := token.ToQuantitySum(64)
		s := reducer.Produce()

		var err error
		require.NotPanics(t, func() {
			_, err = reducer.Reduce(s, (*token.UnspentToken)(nil))
		})

		require.Error(t, err)
	})
}
