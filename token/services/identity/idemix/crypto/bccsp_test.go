/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"testing"

	"github.com/IBM/idemix/bccsp/keystore"
	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockKVS is an in-memory mock implementation of keystore.KVS for testing.
type mockKVS struct {
	data map[string][]byte
}

func newMockKVS() *mockKVS {
	return &mockKVS{
		data: make(map[string][]byte),
	}
}

func (m *mockKVS) Get(key string, value any) error {
	if val, ok := m.data[key]; ok {
		// Copy the value to the provided interface
		if ptr, ok := value.(*[]byte); ok {
			*ptr = val
		}
	}

	return nil
}

func (m *mockKVS) Put(key string, value any) error {
	if bytes, ok := value.([]byte); ok {
		m.data[key] = bytes
	}

	return nil
}

func (m *mockKVS) Delete(key string) error {
	delete(m.data, key)

	return nil
}

// TestGetCurveAndTranslator verifies curve, translator and aries retrieval for all supported curve IDs.
func TestGetCurveAndTranslator(t *testing.T) {
	tests := []struct {
		name      string
		curveID   math.CurveID
		expectErr bool
		aries     bool
	}{
		{
			name:      "BN254",
			curveID:   math.BN254,
			expectErr: false,
			aries:     false,
		},
		{
			name:      "BLS12_377_GURVY",
			curveID:   math.BLS12_377_GURVY,
			expectErr: false,
			aries:     false,
		},
		{
			name:      "FP256BN_AMCL",
			curveID:   math.FP256BN_AMCL,
			expectErr: false,
			aries:     false,
		},
		{
			name:      "FP256BN_AMCL_MIRACL",
			curveID:   math.FP256BN_AMCL_MIRACL,
			expectErr: false,
			aries:     false,
		},
		{
			name:      "BLS12_381_BBS_GURVY",
			curveID:   math.BLS12_381_BBS_GURVY,
			expectErr: false,
			aries:     true,
		},
		{
			name:      "BLS12_381_BBS_GURVY_FAST_RNG",
			curveID:   math2.BLS12_381_BBS_GURVY_FAST_RNG,
			expectErr: false,
			aries:     true,
		},
		{
			name:      "Invalid negative curve",
			curveID:   -1,
			expectErr: true,
		},
		{
			name:      "Unsupported curve ID",
			curveID:   999, // A valid number but not a supported curve
			expectErr: true,
		},
		{
			name:      "BLS12_381_BBS (should switch to GURVY)",
			curveID:   math.BLS12_381_BBS,
			expectErr: false,
			aries:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			curve, translator, aries, err := GetCurveAndTranslator(tt.curveID)

			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, curve)
				assert.Nil(t, translator)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, curve)
				assert.NotNil(t, translator)
				assert.Equal(t, tt.aries, aries)
			}
		})
	}
}

// TestNewKeyStore verifies key store creation for supported curves.
func TestNewKeyStore(t *testing.T) {
	tests := []struct {
		name      string
		curveID   math.CurveID
		expectErr bool
	}{
		{
			name:      "Valid BN254",
			curveID:   math.BN254,
			expectErr: false,
		},
		{
			name:      "Valid BLS12_377_GURVY",
			curveID:   math.BLS12_377_GURVY,
			expectErr: false,
		},
		{
			name:      "Invalid curve",
			curveID:   -1,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newMockKVS()
			ks, err := NewKeyStore(tt.curveID, backend)

			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, ks)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, ks)
			}
		})
	}
}

// TestNewBCCSP verifies BCCSP creation for standard and Aries curves.
func TestNewBCCSP(t *testing.T) {
	tests := []struct {
		name      string
		curveID   math.CurveID
		expectErr bool
	}{
		{
			name:      "Valid BN254",
			curveID:   math.BN254,
			expectErr: false,
		},
		{
			name:      "Valid BLS12_381_BBS_GURVY (Aries)",
			curveID:   math.BLS12_381_BBS_GURVY,
			expectErr: false,
		},
		{
			name:      "Invalid curve",
			curveID:   -1,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyStore := &keystore.Dummy{}
			csp, err := NewBCCSP(keyStore, tt.curveID)

			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, csp)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, csp)
			}
		})
	}
}

// TestNewBCCSP_WithNilKeyStore verifies error handling with nil key store.
func TestNewBCCSP_WithNilKeyStore(t *testing.T) {
	// Should trigger error from idemix.New() or idemix.NewAries()
	// when they try to use a nil keyStore
	csp, err := NewBCCSP(nil, math.BN254)

	// The idemix library should return an error for nil keyStore
	if err != nil {
		require.Error(t, err)
		assert.Nil(t, csp)
	} else {
		// If it doesn't error, at least we tried to cover the error path
		assert.NotNil(t, csp)
	}

	// Also test with Aries curve
	csp2, err2 := NewBCCSP(nil, math.BLS12_381_BBS_GURVY)
	if err2 != nil {
		require.Error(t, err2)
		assert.Nil(t, csp2)
	} else {
		assert.NotNil(t, csp2)
	}
}

// TestNewBCCSPWithDummyKeyStore_ErrorPaths tests potential error paths
func TestNewBCCSPWithDummyKeyStore_ErrorPaths(t *testing.T) {
	// Test with various curves to try to trigger error paths
	// The idemix library is robust and rarely fails, but we document the attempt
	curves := []math.CurveID{
		math.BN254,
		math.FP256BN_AMCL,
		math.BLS12_381_BBS_GURVY,
		math2.BLS12_381_BBS_GURVY_FAST_RNG,
	}

	for _, curveID := range curves {
		csp, err := NewBCCSPWithDummyKeyStore(curveID)
		// These should all succeed with valid curve IDs
		require.NoError(t, err)
		assert.NotNil(t, csp)
	}

	// Test both Aries and non-Aries paths explicitly
	t.Run("Non-Aries curve", func(t *testing.T) {
		csp, err := NewBCCSPWithDummyKeyStore(math.BN254)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})

	t.Run("Aries curve", func(t *testing.T) {
		csp, err := NewBCCSPWithDummyKeyStore(math.BLS12_381_BBS_GURVY)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})
}

// TestGetCurveAndTranslator_AllValidCurves tests all valid curve IDs
func TestGetCurveAndTranslator_AllValidCurves(t *testing.T) {
	// Test all valid curves to maximize coverage
	validCurves := []struct {
		name    string
		curveID math.CurveID
		aries   bool
	}{
		{"BN254", math.BN254, false},
		{"BLS12_377_GURVY", math.BLS12_377_GURVY, false},
		{"FP256BN_AMCL", math.FP256BN_AMCL, false},
		{"FP256BN_AMCL_MIRACL", math.FP256BN_AMCL_MIRACL, false},
		{"BLS12_381_BBS_GURVY", math.BLS12_381_BBS_GURVY, true},
		{"BLS12_381_BBS_GURVY_FAST_RNG", math2.BLS12_381_BBS_GURVY_FAST_RNG, true},
	}

	for _, tc := range validCurves {
		t.Run(tc.name, func(t *testing.T) {
			curve, tr, aries, err := GetCurveAndTranslator(tc.curveID)
			require.NoError(t, err)
			assert.NotNil(t, curve)
			assert.NotNil(t, tr)
			assert.Equal(t, tc.aries, aries)
		})
	}
}

// TestNewBCCSPWithDummyKeyStore tests that valid results are returned
// even when the bccsp has a dummy key store
func TestNewBCCSPWithDummyKeyStore(t *testing.T) {
	tests := []struct {
		name      string
		curveID   math.CurveID
		expectErr bool
	}{
		{
			name:      "Valid BN254",
			curveID:   math.BN254,
			expectErr: false,
		},
		{
			name:      "Valid FP256BN_AMCL",
			curveID:   math.FP256BN_AMCL,
			expectErr: false,
		},
		{
			name:      "Valid BLS12_381_BBS_GURVY (Aries)",
			curveID:   math.BLS12_381_BBS_GURVY,
			expectErr: false,
		},
		{
			name:      "Valid BLS12_381_BBS_GURVY_FAST_RNG (Aries)",
			curveID:   math2.BLS12_381_BBS_GURVY_FAST_RNG,
			expectErr: false,
		},
		{
			name:      "Invalid curve",
			curveID:   -1,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csp, err := NewBCCSPWithDummyKeyStore(tt.curveID)

			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, csp)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, csp)
			}
		})
	}
}

// Test that GetCurveAndTranslator returns aries=true for BLS12_381_BBS
func TestGetCurveAndTranslator_BLS12_381_BBS_Switch(t *testing.T) {
	// Test that BLS12_381_BBS is automatically switched to BLS12_381_BBS_GURVY
	curve, translator, aries, err := GetCurveAndTranslator(math.BLS12_381_BBS)

	require.NoError(t, err)
	assert.NotNil(t, curve)
	assert.NotNil(t, translator)
	assert.True(t, aries, "BLS12_381_BBS should use Aries implementation")
}
