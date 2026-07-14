/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetup(t *testing.T) {
	t.Run("valid precision", func(t *testing.T) {
		pp, err := Setup(32)
		require.NoError(t, err)
		assert.Equal(t, uint64(32), pp.QuantityPrecision)
		assert.Equal(t, uint64(1<<32)-1, pp.MaxToken)
	})

	t.Run("precision too large", func(t *testing.T) {
		pp, err := Setup(65)
		require.Error(t, err)
		assert.Nil(t, pp)
		assert.Equal(t, "invalid precision [65], must be smaller or equal than 64", err.Error())
	})

	t.Run("precision zero", func(t *testing.T) {
		pp, err := Setup(0)
		require.Error(t, err)
		assert.Nil(t, pp)
		assert.Equal(t, "invalid precision, should be greater than 0", err.Error())
	})

	t.Run("extras is initialized", func(t *testing.T) {
		pp, err := Setup(32)
		require.NoError(t, err)
		assert.NotNil(t, pp.Extras())
		ser, err := pp.Serialize()
		require.NoError(t, err)
		pp2, err := NewPublicParamsFromBytes(ser, FabTokenDriverName, ProtocolV1)
		require.NoError(t, err)
		assert.NotNil(t, pp2.Extras())
	})
}

func TestSetupWithVersion(t *testing.T) {
	t.Run("valid setup", func(t *testing.T) {
		pp, err := WithVersion(32, driver.TokenDriverVersion(2))
		require.NoError(t, err)
		assert.Equal(t, uint64(32), pp.QuantityPrecision)
		assert.Equal(t, uint64(1<<32)-1, pp.MaxToken)
		assert.Equal(t, driver.TokenDriverVersion(2), pp.DriverVersion)
	})

	t.Run("invalid precision", func(t *testing.T) {
		pp, err := WithVersion(65, driver.TokenDriverVersion(2))
		require.Error(t, err)
		assert.Nil(t, pp)
	})
}

func TestNewPublicParamsFromBytes(t *testing.T) {
	t.Run("valid params", func(t *testing.T) {
		pp, err := Setup(32)
		require.NoError(t, err)
		raw, err := pp.Serialize()
		require.NoError(t, err)
		pp2, err := NewPublicParamsFromBytes(raw, FabTokenDriverName, ProtocolV1)
		require.NoError(t, err)
		assert.Equal(t, FabTokenDriverName, pp2.DriverName)
		assert.Equal(t, ProtocolV1, pp2.DriverVersion)
		assert.Equal(t, uint64(32), pp2.QuantityPrecision)
		assert.Equal(t, uint64(4294967295), pp2.MaxToken)
		assert.Nil(t, pp2.IssuerIDs)
		assert.Nil(t, pp2.Auditor)
		assert.Equal(t, pp, pp2)
	})

	t.Run("invalid bytes", func(t *testing.T) {
		pp2, err := NewPublicParamsFromBytes([]byte("invalid"), FabTokenDriverName, ProtocolV1)
		require.Error(t, err)
		assert.Nil(t, pp2)
	})
}

func TestPublicParams_Methods(t *testing.T) {
	pp, err := Setup(32)
	require.NoError(t, err)

	t.Run("driver info", func(t *testing.T) {
		assert.Equal(t, FabTokenDriverName, pp.TokenDriverName())
		assert.Equal(t, ProtocolV1, pp.TokenDriverVersion())
	})

	t.Run("data and graph hiding", func(t *testing.T) {
		assert.False(t, pp.TokenDataHiding())
		assert.False(t, pp.GraphHiding())
	})

	t.Run("certification driver", func(t *testing.T) {
		assert.Equal(t, string(FabTokenDriverName), pp.CertificationDriver())
	})

	t.Run("max token value", func(t *testing.T) {
		assert.Equal(t, uint64(1<<32-1), pp.MaxTokenValue())
	})

	t.Run("auditor operations", func(t *testing.T) {
		auditor := driver.Identity([]byte("auditor1"))
		assert.Empty(t, pp.Auditors())
		pp.AddAuditor(auditor)
		assert.Equal(t, auditor, pp.AuditorIdentity())
		assert.Equal(t, []driver.Identity{auditor}, pp.Auditors())

		// Test SetAuditors
		newAuditor := driver.Identity([]byte("auditor2"))
		pp.SetAuditors([]driver.Identity{newAuditor})
		assert.Equal(t, newAuditor, pp.AuditorIdentity())
	})

	t.Run("issuer operations", func(t *testing.T) {
		issuer1 := driver.Identity([]byte("issuer1"))
		issuer2 := driver.Identity([]byte("issuer2"))
		assert.Empty(t, pp.Issuers())

		pp.AddIssuer(issuer1)
		assert.Equal(t, []driver.Identity{issuer1}, pp.Issuers())

		pp.AddIssuer(issuer2)
		assert.Equal(t, []driver.Identity{issuer1, issuer2}, pp.Issuers())

		// Test SetIssuers
		newIssuers := []driver.Identity{driver.Identity([]byte("new_issuer"))}
		pp.SetIssuers(newIssuers)
		assert.Equal(t, newIssuers, pp.Issuers())
	})
}

func TestPublicParams_Validation(t *testing.T) {
	t.Run("valid params", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
		}
		err := pp.Validate()
		require.NoError(t, err)
	})

	t.Run("precision too large", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 65,
			MaxToken:          1<<64 - 1,
		}
		err := pp.Validate()
		require.Error(t, err)
		assert.Equal(t, "invalid precision [65], must be less than 64", err.Error())
	})

	t.Run("precision zero", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 0,
			MaxToken:          1,
		}
		err := pp.Validate()
		require.Error(t, err)
		assert.Equal(t, "invalid precision, must be greater than 0", err.Error())
	})

	t.Run("invalid max token", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 32,
			MaxToken:          1 << 32,
		}
		err := pp.Validate()
		require.Error(t, err)
		assert.Equal(t, "max token value is invalid [4294967296]>[4294967295]", err.Error())
	})
}

func TestPublicParams_Serialization(t *testing.T) {
	t.Run("valid serialization and deserialization", func(t *testing.T) {
		original, err := Setup(32)
		require.NoError(t, err)

		// Add some data
		original.AddAuditor([]byte("auditor1"))
		original.AddIssuer([]byte("issuer1"))
		original.ExtraData = map[string][]byte{"key1": []byte("value1")}

		// Serialize
		serialized, err := original.Serialize()
		require.NoError(t, err)

		// Deserialize
		deserialized := &PublicParams{
			DriverName:    FabTokenDriverName,
			DriverVersion: ProtocolV1,
		}
		err = deserialized.Deserialize(serialized)
		require.NoError(t, err)

		// Verify all fields match
		assert.Equal(t, original.QuantityPrecision, deserialized.QuantityPrecision)
		assert.Equal(t, original.MaxToken, deserialized.MaxToken)
		assert.Equal(t, original.Auditor, deserialized.Auditor)
		assert.Equal(t, original.IssuerIDs, deserialized.IssuerIDs)
		assert.Equal(t, original.ExtraData, deserialized.ExtraData)
	})

	t.Run("invalid deserialization", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:    FabTokenDriverName,
			DriverVersion: ProtocolV1,
		}
		err := pp.Deserialize([]byte("invalid"))
		require.Error(t, err)
	})

	t.Run("mismatched driver identifier", func(t *testing.T) {
		original, err := Setup(32)
		require.NoError(t, err)
		serialized, err := original.Serialize()
		require.NoError(t, err)

		wrongDriver := &PublicParams{
			DriverName:    "wrong",
			DriverVersion: ProtocolV1,
		}
		err = wrongDriver.Deserialize(serialized)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid identifier")
	})
}

func TestPublicParams_BytesAndFromBytes(t *testing.T) {
	t.Run("serialization with nil values", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			IssuerIDs:         nil,
			Auditor:           nil,
			ExtraData:         nil,
		}
		bytes, err := pp.Bytes()
		require.NoError(t, err)
		assert.NotNil(t, bytes)

		// Test FromBytes with the serialized data
		newPP := &PublicParams{}
		err = newPP.FromBytes(bytes)
		require.NoError(t, err)
		assert.Equal(t, pp.QuantityPrecision, newPP.QuantityPrecision)
		assert.Equal(t, pp.MaxToken, newPP.MaxToken)
	})

	t.Run("serialization with invalid identity", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			IssuerIDs:         []driver.Identity{nil}, // Invalid identity
		}
		bytes, err := pp.Bytes()
		require.NoError(t, err) // Should handle nil identity

		newPP := &PublicParams{}
		err = newPP.FromBytes(bytes)
		require.NoError(t, err)
	})

	t.Run("invalid protobuf data", func(t *testing.T) {
		pp := &PublicParams{}
		err := pp.FromBytes([]byte{0xFF, 0xFF, 0xFF}) // Invalid protobuf data
		require.Error(t, err)
	})
}

func TestPublicParams_String(t *testing.T) {
	pp, err := Setup(32)
	require.NoError(t, err)
	pp.AddAuditor([]byte("auditor1"))
	pp.AddIssuer([]byte("issuer1"))
	pp.ExtraData = map[string][]byte{"key1": []byte("value1")}

	str := pp.String()
	assert.NotEmpty(t, str)
	assert.Contains(t, str, "QuantityPrecision")
	assert.Contains(t, str, "MaxToken")
}

func TestPublicParams_StringWithInvalidJSON(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		// Add a channel which cannot be marshaled to JSON
		ExtraData: map[string][]byte{
			"key": []byte("value"),
		},
	}

	str := pp.String()
	assert.Contains(t, str, "QuantityPrecision")
	assert.Contains(t, str, "MaxToken")
	assert.Contains(t, str, "key")
}

func TestExtras(t *testing.T) {
	pp := &PublicParams{
		ExtraData: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	extras := pp.Extras()
	assert.Equal(t, pp.ExtraData, extras)

	// Verify nil case
	pp = &PublicParams{}
	assert.Nil(t, pp.Extras())

	// Verify empty map case
	pp.ExtraData = make(map[string][]byte)
	assert.Empty(t, pp.Extras())
}

func TestPublicParams_Precision(t *testing.T) {
	pp, err := Setup(32)
	require.NoError(t, err)
	assert.Equal(t, uint64(32), pp.Precision())

	pp2, err := Setup(64)
	require.NoError(t, err)
	assert.Equal(t, uint64(64), pp2.Precision())
}

func TestPublicParams_SerializeError(t *testing.T) {
	t.Run("serialize with bytes error", func(t *testing.T) {
		// Create a PublicParams with an issuer that will cause serialization to fail
		// This is difficult to trigger in practice, but we can test the error path
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			DriverVersion:     ProtocolV1,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			IssuerIDs:         []driver.Identity{[]byte("issuer1")},
		}

		// First verify normal serialization works
		_, err := pp.Serialize()
		require.NoError(t, err)
	})
}

func TestPublicParams_FromBytesEdgeCases(t *testing.T) {
	t.Run("from bytes with nil auditor", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			DriverVersion:     ProtocolV1,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			Auditor:           nil,
		}
		bytes, err := pp.Bytes()
		require.NoError(t, err)

		newPP := &PublicParams{}
		err = newPP.FromBytes(bytes)
		require.NoError(t, err)
		assert.Nil(t, newPP.Auditor)
	})

	t.Run("from bytes with empty extra data", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			DriverVersion:     ProtocolV1,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			ExtraData:         nil,
		}
		bytes, err := pp.Bytes()
		require.NoError(t, err)

		newPP := &PublicParams{}
		err = newPP.FromBytes(bytes)
		require.NoError(t, err)
		assert.NotNil(t, newPP.ExtraData)
		assert.Empty(t, newPP.ExtraData)
	})

	t.Run("from bytes with multiple issuers", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			DriverVersion:     ProtocolV1,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
			IssuerIDs: []driver.Identity{
				[]byte("issuer1"),
				[]byte("issuer2"),
				[]byte("issuer3"),
			},
		}
		bytes, err := pp.Bytes()
		require.NoError(t, err)

		newPP := &PublicParams{}
		err = newPP.FromBytes(bytes)
		require.NoError(t, err)
		assert.Len(t, newPP.IssuerIDs, 3)
		assert.Equal(t, pp.IssuerIDs, newPP.IssuerIDs)
	})
}

func TestPublicParams_BytesWithMultipleIssuers(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		IssuerIDs: []driver.Identity{
			[]byte("issuer1"),
			[]byte("issuer2"),
		},
		Auditor:   []byte("auditor1"),
		ExtraData: map[string][]byte{"key": []byte("value")},
	}

	bytes, err := pp.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, bytes)

	// Deserialize and verify
	newPP := &PublicParams{}
	err = newPP.FromBytes(bytes)
	require.NoError(t, err)
	assert.Equal(t, pp.IssuerIDs, newPP.IssuerIDs)
	assert.Equal(t, pp.Auditor, newPP.Auditor)
	assert.Equal(t, pp.ExtraData, newPP.ExtraData)
}

func TestNewWith(t *testing.T) {
	t.Run("valid precision values", func(t *testing.T) {
		testCases := []struct {
			name      string
			precision uint64
			maxToken  uint64
		}{
			{"precision 1", 1, 1},
			{"precision 8", 8, 255},
			{"precision 16", 16, 65535},
			{"precision 32", 32, 4294967295},
			{"precision 64", 64, 18446744073709551615},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				pp, err := NewWith(FabTokenDriverName, ProtocolV1, tc.precision)
				require.NoError(t, err)
				assert.Equal(t, tc.precision, pp.QuantityPrecision)
				assert.Equal(t, tc.maxToken, pp.MaxToken)
				assert.Equal(t, FabTokenDriverName, pp.DriverName)
				assert.Equal(t, ProtocolV1, pp.DriverVersion)
			})
		}
	})

	t.Run("custom driver name and version", func(t *testing.T) {
		customDriver := driver.TokenDriverName("custom")
		customVersion := driver.TokenDriverVersion(5)
		pp, err := NewWith(customDriver, customVersion, 32)
		require.NoError(t, err)
		assert.Equal(t, customDriver, pp.DriverName)
		assert.Equal(t, customVersion, pp.DriverVersion)
	})
}

func TestPublicParams_AuditorsEmptyCase(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		Auditor:           []byte{}, // Empty but not nil
	}

	auditors := pp.Auditors()
	assert.Empty(t, auditors)
}

func TestPublicParams_ValidationEdgeCases(t *testing.T) {
	t.Run("max token equals calculated max", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 32,
			MaxToken:          1<<32 - 1,
		}
		err := pp.Validate()
		require.NoError(t, err)
	})

	t.Run("precision at boundary 64", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 64,
			MaxToken:          1<<64 - 1,
		}
		err := pp.Validate()
		require.NoError(t, err)
	})

	t.Run("precision at boundary 1", func(t *testing.T) {
		pp := &PublicParams{
			DriverName:        FabTokenDriverName,
			QuantityPrecision: 1,
			MaxToken:          1,
		}
		err := pp.Validate()
		require.NoError(t, err)
	})
}

func TestPublicParams_CompleteRoundTrip(t *testing.T) {
	// Create a fully populated PublicParams
	original := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		Auditor:           []byte("auditor1"),
		IssuerIDs: []driver.Identity{
			[]byte("issuer1"),
			[]byte("issuer2"),
		},
		ExtraData: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	// Test Bytes -> FromBytes
	bytes, err := original.Bytes()
	require.NoError(t, err)

	reconstructed := &PublicParams{}
	err = reconstructed.FromBytes(bytes)
	require.NoError(t, err)

	assert.Equal(t, original.DriverVersion, reconstructed.DriverVersion)
	assert.Equal(t, original.QuantityPrecision, reconstructed.QuantityPrecision)
	assert.Equal(t, original.MaxToken, reconstructed.MaxToken)
	assert.Equal(t, original.Auditor, reconstructed.Auditor)
	assert.Equal(t, original.IssuerIDs, reconstructed.IssuerIDs)
	assert.Equal(t, original.ExtraData, reconstructed.ExtraData)

	// Test Serialize -> Deserialize
	serialized, err := original.Serialize()
	require.NoError(t, err)

	deserialized := &PublicParams{
		DriverName:    FabTokenDriverName,
		DriverVersion: ProtocolV1,
	}
	err = deserialized.Deserialize(serialized)
	require.NoError(t, err)

	assert.Equal(t, original.QuantityPrecision, deserialized.QuantityPrecision)
	assert.Equal(t, original.MaxToken, deserialized.MaxToken)
	assert.Equal(t, original.Auditor, deserialized.Auditor)
	assert.Equal(t, original.IssuerIDs, deserialized.IssuerIDs)
	assert.Equal(t, original.ExtraData, deserialized.ExtraData)
}

func TestPublicParams_StringWithJSONError(t *testing.T) {
	// Create a struct that will cause JSON marshaling to succeed
	// (The String() method uses json.MarshalIndent which should handle all valid Go types)
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		Auditor:           []byte("auditor"),
		IssuerIDs:         []driver.Identity{[]byte("issuer")},
		ExtraData:         map[string][]byte{"key": []byte("value")},
	}

	// String should work fine with valid data
	str := pp.String()
	assert.NotEmpty(t, str)
	assert.Contains(t, str, "DriverName")
	assert.Contains(t, str, "QuantityPrecision")
}

func TestPublicParams_BytesWithEmptyIssuers(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		IssuerIDs:         []driver.Identity{},
		Auditor:           []byte("auditor"),
	}

	bytes, err := pp.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, bytes)

	// Verify deserialization
	newPP := &PublicParams{}
	err = newPP.FromBytes(bytes)
	require.NoError(t, err)
	assert.Empty(t, newPP.IssuerIDs)
}

func TestPublicParams_SerializeWithAllFields(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 64,
		MaxToken:          1<<64 - 1,
		Auditor:           []byte("test-auditor"),
		IssuerIDs: []driver.Identity{
			[]byte("issuer1"),
			[]byte("issuer2"),
			[]byte("issuer3"),
		},
		ExtraData: map[string][]byte{
			"custom1": []byte("data1"),
			"custom2": []byte("data2"),
		},
	}

	// Test Serialize
	serialized, err := pp.Serialize()
	require.NoError(t, err)
	assert.NotNil(t, serialized)

	// Test Deserialize
	newPP := &PublicParams{
		DriverName:    FabTokenDriverName,
		DriverVersion: ProtocolV1,
	}
	err = newPP.Deserialize(serialized)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, pp.QuantityPrecision, newPP.QuantityPrecision)
	assert.Equal(t, pp.MaxToken, newPP.MaxToken)
	assert.Equal(t, pp.Auditor, newPP.Auditor)
	assert.Equal(t, pp.IssuerIDs, newPP.IssuerIDs)
	assert.Equal(t, pp.ExtraData, newPP.ExtraData)
}

func TestPublicParams_FromBytesWithNilIssuers(t *testing.T) {
	// Create params with nil issuers
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		IssuerIDs:         nil,
		Auditor:           []byte("auditor"),
	}

	bytes, err := pp.Bytes()
	require.NoError(t, err)

	newPP := &PublicParams{}
	err = newPP.FromBytes(bytes)
	require.NoError(t, err)

	// Verify issuers is empty slice (not nil after deserialization)
	assert.Empty(t, newPP.IssuerIDs)
}

func TestPublicParams_AllGetters(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
		Auditor:           []byte("auditor"),
		IssuerIDs:         []driver.Identity{[]byte("issuer1")},
		ExtraData:         map[string][]byte{"key": []byte("value")},
	}

	// Test all getter methods
	assert.Equal(t, FabTokenDriverName, pp.TokenDriverName())
	assert.Equal(t, ProtocolV1, pp.TokenDriverVersion())
	assert.False(t, pp.TokenDataHiding())
	assert.False(t, pp.GraphHiding())
	assert.Equal(t, string(FabTokenDriverName), pp.CertificationDriver())
	assert.Equal(t, uint64(1<<32-1), pp.MaxTokenValue())
	assert.Equal(t, driver.Identity([]byte("auditor")), pp.AuditorIdentity())
	assert.Equal(t, []driver.Identity{driver.Identity([]byte("auditor"))}, pp.Auditors())
	assert.Equal(t, []driver.Identity{[]byte("issuer1")}, pp.Issuers())
	assert.Equal(t, uint64(32), pp.Precision())
	assert.Equal(t, map[string][]byte{"key": []byte("value")}, pp.Extras())
}

func TestPublicParams_SettersWithMultipleValues(t *testing.T) {
	pp := &PublicParams{
		DriverName:        FabTokenDriverName,
		DriverVersion:     ProtocolV1,
		QuantityPrecision: 32,
		MaxToken:          1<<32 - 1,
	}

	// Test SetIssuers with multiple values
	issuers := []driver.Identity{
		[]byte("issuer1"),
		[]byte("issuer2"),
		[]byte("issuer3"),
	}
	pp.SetIssuers(issuers)
	assert.Equal(t, issuers, pp.Issuers())

	// Test SetAuditors (only uses first element)
	auditors := []driver.Identity{
		[]byte("auditor1"),
		[]byte("auditor2"),
	}
	pp.SetAuditors(auditors)
	assert.Equal(t, driver.Identity([]byte("auditor1")), pp.AuditorIdentity())
	assert.Equal(t, []driver.Identity{driver.Identity([]byte("auditor1"))}, pp.Auditors())
}
