/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test file mainly tests usage of various BCCSP configurations as implemented in
// token/services/identity/x509/crypto/config.go
// token/services/identity/x509/crypto/bccsp.go
func TestGetBCCSPFromConf(t *testing.T) {
	keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())

	// Test creating a BCCSP instance using a nil config => default taken from `SW` provider
	t.Run("Nil Config - Default SW", func(t *testing.T) {
		csp, err := GetBCCSPFromConf(nil, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})

	// Test creating a BCCSP instance using a config for the `SW` provider
	t.Run("SW Provider", func(t *testing.T) {
		conf := &BCCSP{
			Default: "SW",
			SW: &SoftwareProvider{
				Hash:     "SHA2",
				Security: 256,
			},
		}
		csp, err := GetBCCSPFromConf(conf, keyStore)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})

	// Test failure getting a BCCSP instance using a config with an invalid default
	t.Run("Invalid Provider", func(t *testing.T) {
		conf := &BCCSP{
			Default: "INVALID",
		}
		_, err := GetBCCSPFromConf(conf, keyStore)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid BCCSP.Default")
	})

	// Test failure getting a BCCSP with the conf default set to PKCS11
	// but the conf PKCS11 field left nil
	t.Run("PKCS11 Without Config", func(t *testing.T) {
		conf := &BCCSP{
			Default: "PKCS11",
		}
		_, err := GetBCCSPFromConf(conf, keyStore)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing configuration")
	})
}

func TestGetDefaultBCCSP(t *testing.T) {
	// Test getting a default (i.e. `SW` provider) bccsp with a given keyStore
	t.Run("With KeyStore", func(t *testing.T) {
		keyStore := csp.NewKVSStore(kvs.NewTrackedMemory())
		csp, err := GetDefaultBCCSP(keyStore)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})

	// Test getting a default (i.e. `SW` provider) bccsp when no keyStore is provided
	t.Run("Nil KeyStore", func(t *testing.T) {
		csp, err := GetDefaultBCCSP(nil)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})
}

func TestSKIMapper(t *testing.T) {
	// Test using a skiMapper to map a given SKI (Subject Key Identifier) to an ID
	// given a pre-defined map for that SKI
	t.Run("With Matching SKI", func(t *testing.T) {
		p11Opts := PKCS11{
			KeyIDs: []KeyIDMapping{
				{SKI: "abcd", ID: "key1"},
			},
		}
		mapper := skiMapper(p11Opts)
		result := mapper([]byte{0xab, 0xcd})
		assert.Equal(t, []byte("key1"), result)
	})

	// Test using a skiMapper to map a given SKI (Subject Key Identifier) to an ID
	// given a pre-defined map setting a fallback when the SKI isn't found in the map
	t.Run("With AltID", func(t *testing.T) {
		p11Opts := PKCS11{
			AltID: "altkey",
		}
		mapper := skiMapper(p11Opts)
		result := mapper([]byte{0x12, 0x34})
		assert.Equal(t, []byte("altkey"), result)
	})

	// Test using a skiMapper to map a given SKI (Subject Key Identifier) to an ID
	// when that SKI isn't found in the map. In such a case the original SKI is returned.
	t.Run("No Match Returns SKI", func(t *testing.T) {
		p11Opts := PKCS11{}
		mapper := skiMapper(p11Opts)
		ski := []byte{0x12, 0x34}
		result := mapper(ski)
		assert.Equal(t, ski, result)
	})
}

func TestUnmarshalConfig(t *testing.T) {
	// Test that unmarshlaling a marshalled Config returns the expected value
	t.Run("Valid Config", func(t *testing.T) {
		// Create a simple config
		origConfig := &Config{
			Version: 1,
		}
		data, err := MarshalConfig(origConfig)
		require.NoError(t, err)

		config, err := UnmarshalConfig(data)
		require.NoError(t, err)
		assert.Equal(t, uint32(1), config.Version)
	})

	// Test failure to unmarshal an invalid raw config
	t.Run("Invalid Data", func(t *testing.T) {
		_, err := UnmarshalConfig([]byte("invalid"))
		require.Error(t, err)
	})
}

// Test that unmarshlaling a marshalled Config returns the original fields
func TestMarshalConfig(t *testing.T) {
	config := &Config{
		Version: 1,
	}
	data, err := MarshalConfig(config)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify we can unmarshal it back and get the same version
	unmarshalled, err := UnmarshalConfig(data)
	require.NoError(t, err)
	assert.Equal(t, config.Version, unmarshalled.Version)
}

func TestToBCCSPOpts(t *testing.T) {
	// Test creating BCCSP options structure from a specified string map
	t.Run("Valid Options", func(t *testing.T) {
		input := map[string]any{
			"BCCSP": map[string]any{
				"Default": "SW",
				"SW": map[string]any{
					"Hash":     "SHA2",
					"Security": 256,
				},
			},
		}
		opts, err := ToBCCSPOpts(input)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, "SW", opts.Default)
	})

	// Test that reating BCCSP options structure from an empty string map
	// results in a nil options object
	t.Run("Empty Options", func(t *testing.T) {
		opts, err := ToBCCSPOpts(map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})
}

// Test creating a pkcs11.PKCS11Opts object from options defined in PKCS11
func TestToPKCS11OptsOpts(t *testing.T) {
	input := &PKCS11{
		Security:       256,
		Hash:           "SHA2",
		Library:        "/usr/lib/libpkcs11.so",
		Label:          "test",
		Pin:            "1234",
		SoftwareVerify: true,
		Immutable:      false,
		AltID:          "alt",
		KeyIDs: []KeyIDMapping{
			{SKI: "ski1", ID: "id1"},
		},
	}

	result := ToPKCS11OptsOpts(input)
	assert.Equal(t, 256, result.Security)
	assert.Equal(t, "SHA2", result.Hash)
	assert.Equal(t, "/usr/lib/libpkcs11.so", result.Library)
	assert.Equal(t, "test", result.Label)
	assert.Equal(t, "1234", result.Pin)
	assert.True(t, result.SoftwareVerify)
	assert.False(t, result.Immutable)
	assert.Equal(t, "alt", result.AltID)
	assert.Len(t, result.KeyIDs, 1)
}

func TestBCCSPOpts(t *testing.T) {
	// Test that BCCSP options created for the "SW" provider are as expected
	t.Run("SW Provider", func(t *testing.T) {
		opts, err := BCCSPOpts("SW")
		require.NoError(t, err)
		assert.Equal(t, "SW", opts.Default)
		assert.NotNil(t, opts.SW)
		assert.Equal(t, "SHA2", opts.SW.Hash)
		assert.Equal(t, 256, opts.SW.Security)
	})
}

func TestGetHashOpt(t *testing.T) {
	// Test getting bccsp options for the SHA2 hash family
	t.Run("SHA2", func(t *testing.T) {
		opt, err := getHashOpt(bccsp.SHA2)
		require.NoError(t, err)
		assert.NotNil(t, opt)
		// Verify it returns SHA256 hash option
		expectedOpt, _ := bccsp.GetHashOpt(bccsp.SHA256)
		assert.Equal(t, expectedOpt, opt)
	})

	// Test getting bccsp options for the SHA3 hash family
	t.Run("SHA3", func(t *testing.T) {
		opt, err := getHashOpt(bccsp.SHA3)
		require.NoError(t, err)
		assert.NotNil(t, opt)
		// Verify it returns SHA3_256 hash option
		expectedOpt, _ := bccsp.GetHashOpt(bccsp.SHA3_256)
		assert.Equal(t, expectedOpt, opt)
	})

	// Test failure getting bccsp options for an unrecognized hash family
	t.Run("Unknown Hash Family", func(t *testing.T) {
		_, err := getHashOpt("UNKNOWN")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hash family not recognized")
	})
}

// Test success marshalling a bccsp Config in which the Config field isn't set
func TestMarshalConfig_EmptyConfig(t *testing.T) {
	// Test with empty config - should still marshal successfully
	config := &Config{
		Version: 1,
	}
	data, err := MarshalConfig(config)
	require.NoError(t, err)
	assert.NotNil(t, data)
}

// Test that creating a bccsp with a config for default "PKCS11"
// requires a setting of the PKCS11 of the config
func TestGetPKCS11BCCSP_Error(t *testing.T) {
	t.Run("Nil PKCS11 Config", func(t *testing.T) {
		conf := &BCCSP{
			Default: "PKCS11",
			PKCS11:  nil,
		}
		_, err := GetPKCS11BCCSP(conf, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing configuration")
	})
}

func TestBCCSPOpts_PKCS11(t *testing.T) {
	// Test that an attempt to create BCCSPOpts options for the PKCS11 provider
	// now panics because the needed library is not included in the build
	t.Run("PKCS11 Provider Without Library", func(t *testing.T) {
		// This will panic because PKCS11 is not included in build
		// We test that the panic happens as expected
		defer func() {
			r := recover()
			assert.NotNil(t, r)
			assert.Contains(t, r.(string), "pkcs11 not included")
		}()

		_, _ = BCCSPOpts("PKCS11")
		t.Fatal("Expected panic but didn't get one")
	})
}

func TestGetPKCS11BCCSP_WithKeyStore(t *testing.T) {
	// Test that an attempt to create a HSM-based BCCSP for the PKCS11 provider
	// using a valid (mock) keyStore
	// now panics because the needed library is not included in the build
	t.Run("With Valid KeyStore", func(t *testing.T) {
		conf := &BCCSP{
			Default: "PKCS11",
			PKCS11: &PKCS11{
				Library:  "/usr/lib/softhsm/libsofthsm2.so",
				Label:    "test",
				Pin:      "1234",
				Security: 256,
				Hash:     "SHA2",
			},
		}

		ks := &mocks.KeyStore{}
		ks.ReadOnlyReturns(false)
		ks.GetKeyReturns(nil, errors.New("not found"))
		ks.StoreKeyReturns(nil)

		// This will panic because PKCS11 is not included in build
		defer func() {
			r := recover()
			assert.NotNil(t, r)
			// Verify no KeyStore methods were called before panic
			assert.Equal(t, 0, ks.ReadOnlyCallCount())
			assert.Equal(t, 0, ks.GetKeyCallCount())
			assert.Equal(t, 0, ks.StoreKeyCallCount())
		}()

		_, _ = GetPKCS11BCCSP(conf, ks)
		t.Fatal("Expected panic but didn't get one")
	})
}

func TestGetDefaultBCCSP_WithNilKeyStore(t *testing.T) {
	// Test that getting a default BCCSP without setting a KeyStore still works
	// because a new dummy ks is used in this case
	t.Run("Nil KeyStore Creates Dummy", func(t *testing.T) {
		csp, err := GetDefaultBCCSP(nil)
		require.NoError(t, err)
		assert.NotNil(t, csp)
	})
}

func TestToBCCSPOpts_EdgeCases(t *testing.T) {
	// Test that creating bccsp options from an empty map works but returns nil
	t.Run("Empty Map", func(t *testing.T) {
		opts, err := ToBCCSPOpts(map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, opts)
	})

	// Test that creating bccsp options using a map creates the BCCSP options object
	// with the expected fields
	t.Run("With BCCSP Config", func(t *testing.T) {
		input := map[string]any{
			"BCCSP": map[string]any{
				"Default": "SW",
				"SW": map[string]any{
					"Hash":     "SHA2",
					"Security": 256,
				},
			},
		}
		opts, err := ToBCCSPOpts(input)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, "SW", opts.Default)
	})
}

// Test failure in an attempt to create bccsp hash options for an unrecognized hash family
func TestGetHashOpt_InvalidFamily(t *testing.T) {
	_, err := getHashOpt("INVALID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not recognized")
}
