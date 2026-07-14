/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"bytes"
	"encoding/asn1"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFastMarshalTokenRequestForSigning_Compatibility verifies that the fast marshaller
// produces identical output to encoding/asn1 for TokenRequest structures with typed actions
func TestFastMarshalTokenRequestForSigning_Compatibility(t *testing.T) {
	testCases := []struct {
		name    string
		actions []*TypedAction
	}{
		{
			name:    "Empty",
			actions: []*TypedAction{},
		},
		{
			name: "Single issue",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		},
		{
			name: "Single transfer",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
			},
		},
		{
			name: "Multiple issues then transfers",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer2")},
			},
		},
		{
			name: "Mixed order: issue, transfer, issue, transfer",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer2")},
			},
		},
		{
			name: "Large data",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 1000)},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 2000)},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 1500)},
			},
		},
		{
			name: "Many small items",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("a")},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("b")},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("c")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("2")},
			},
		},
		{
			name: "Empty byte slices",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{}},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{}},
			},
		},
		{
			name: "Binary data",
			actions: []*TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{0x00, 0x01, 0x02, 0xFF}},
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{0xDE, 0xAD, 0xBE, 0xEF}},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{0x12, 0x34, 0x56, 0x78}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fast marshaller output
			fastResult, err := fastMarshalTokenRequestForSigning(tc.actions)
			require.NoError(t, err)

			// Standard ASN.1 marshaller output
			type typedAction struct {
				Type int
				Data []byte
			}
			type tokenRequestForSigning struct {
				Actions []typedAction
			}

			// Convert TypedAction to ASN.1 structure
			asn1Actions := make([]typedAction, len(tc.actions))
			for i, action := range tc.actions {
				// Map protobuf enum to ASN.1 integer
				var actionType int
				switch action.Type {
				case request.ActionType_ACTION_TYPE_ISSUE:
					actionType = 0
				case request.ActionType_ACTION_TYPE_TRANSFER:
					actionType = 1
				default:
					actionType = 0
				}
				asn1Actions[i] = typedAction{
					Type: actionType,
					Data: action.Raw,
				}
			}

			stdResult, err := asn1.Marshal(tokenRequestForSigning{
				Actions: asn1Actions,
			})
			require.NoError(t, err)

			// Results must be identical
			assert.Equal(t, stdResult, fastResult, "Fast marshaller output must match standard ASN.1")
		})
	}
}

// TestFastMarshalSignatureMessageV1_Compatibility verifies that the fast marshaller
// produces identical output to encoding/asn1 for SignatureMessage structures
func TestFastMarshalSignatureMessageV1_Compatibility(t *testing.T) {
	testCases := []struct {
		name    string
		request []byte
		anchor  []byte
	}{
		{
			name:    "Small data",
			request: []byte("request"),
			anchor:  []byte("anchor"),
		},
		{
			name:    "Empty request",
			request: []byte{},
			anchor:  []byte("anchor"),
		},
		{
			name:    "Large request",
			request: make([]byte, 5000),
			anchor:  []byte("anchor"),
		},
		{
			name:    "Large anchor",
			request: []byte("request"),
			anchor:  make([]byte, 128),
		},
		{
			name:    "Binary data",
			request: []byte{0x00, 0x01, 0x02, 0xFF, 0xDE, 0xAD, 0xBE, 0xEF},
			anchor:  []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0},
		},
		{
			name:    "Length requiring 2-byte encoding",
			request: make([]byte, 200),
			anchor:  []byte("anchor"),
		},
		{
			name:    "Length requiring 3-byte encoding",
			request: make([]byte, 70000),
			anchor:  []byte("anchor"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fast marshaller output
			fastResult, err := fastMarshalSignatureMessageV1(tc.request, tc.anchor)
			require.NoError(t, err)

			// Standard ASN.1 marshaller output
			type signatureMessage struct {
				Request []byte
				Anchor  []byte
			}
			stdResult, err := asn1.Marshal(signatureMessage{
				Request: tc.request,
				Anchor:  tc.anchor,
			})
			require.NoError(t, err)

			// Results must be identical
			assert.Equal(t, stdResult, fastResult, "Fast marshaller output must match standard ASN.1")
		})
	}
}

// TestFastMarshalTokenRequestForSigning_EdgeCases tests edge cases and boundary conditions
func TestFastMarshalTokenRequestForSigning_EdgeCases(t *testing.T) {
	t.Run("Nil slices", func(t *testing.T) {
		fast, err := fastMarshalTokenRequestForSigning(nil)
		require.NoError(t, err)

		type typedAction struct {
			Type int
			Data []byte
		}
		type tokenRequestForSigning struct {
			Actions []typedAction
		}
		std, err := asn1.Marshal(tokenRequestForSigning{})
		require.NoError(t, err)

		assert.Equal(t, std, fast)
	})

	t.Run("127-byte data (short form boundary)", func(t *testing.T) {
		data := make([]byte, 127)
		actions := []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: data},
		}
		fast, err := fastMarshalTokenRequestForSigning(actions)
		require.NoError(t, err)

		type typedAction struct {
			Type int
			Data []byte
		}
		type tokenRequestForSigning struct {
			Actions []typedAction
		}
		std, err := asn1.Marshal(tokenRequestForSigning{
			Actions: []typedAction{{Type: 0, Data: data}},
		})
		require.NoError(t, err)

		assert.Equal(t, std, fast)
	})

	t.Run("128-byte data (long form boundary)", func(t *testing.T) {
		data := make([]byte, 128)
		actions := []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: data},
		}
		fast, err := fastMarshalTokenRequestForSigning(actions)
		require.NoError(t, err)

		type typedAction struct {
			Type int
			Data []byte
		}
		type tokenRequestForSigning struct {
			Actions []typedAction
		}
		std, err := asn1.Marshal(tokenRequestForSigning{
			Actions: []typedAction{{Type: 0, Data: data}},
		})
		require.NoError(t, err)

		assert.Equal(t, std, fast)
	})
}

// TestEncodedLength verifies the length encoding calculation
func TestEncodedLength(t *testing.T) {
	testCases := []struct {
		length   int
		expected int
	}{
		{0, 1},        // Short form
		{127, 1},      // Short form max
		{128, 2},      // Long form 1 byte
		{255, 2},      // Long form 1 byte max
		{256, 3},      // Long form 2 bytes
		{65535, 3},    // Long form 2 bytes max
		{65536, 4},    // Long form 3 bytes
		{16777215, 4}, // Long form 3 bytes max
		{16777216, 5}, // Long form 4 bytes
	}

	for _, tc := range testCases {
		// #nosec G115 -- tc.length is a test value, converting to string for test name
		t.Run(fmt.Sprintf("length_%d", tc.length), func(t *testing.T) {
			result := encodedLength(tc.length)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestAppendLength verifies the length encoding implementation
func TestAppendLength(t *testing.T) {
	testCases := []struct {
		name     string
		length   int
		expected []byte
	}{
		{"Zero", 0, []byte{0x00}},
		{"Short form max", 127, []byte{0x7F}},
		{"Long form 1 byte", 128, []byte{0x81, 0x80}},
		{"Long form 1 byte max", 255, []byte{0x81, 0xFF}},
		{"Long form 2 bytes", 256, []byte{0x82, 0x01, 0x00}},
		{"Long form 2 bytes max", 65535, []byte{0x82, 0xFF, 0xFF}},
		{"Long form 3 bytes", 65536, []byte{0x83, 0x01, 0x00, 0x00}},
		{"Long form 4 bytes", 16777216, []byte{0x84, 0x01, 0x00, 0x00, 0x00}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := appendLength(nil, tc.length)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestMarshalToMessageToSignV1_UsesFastMarshaller verifies that V1 uses the fast marshaller
func TestMarshalToMessageToSignV1_UsesFastMarshaller(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}
	anchor := []byte("test-anchor")

	// Get V1 output (should use fast marshaller)
	v1Result, err := tr.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	// Manually construct expected output using standard ASN.1
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}

	// Convert TypedAction to ASN.1 structure
	asn1Actions := make([]typedAction, len(tr.Actions))
	for i, action := range tr.Actions {
		var actionType int
		switch action.Type {
		case request.ActionType_ACTION_TYPE_ISSUE:
			actionType = 0
		case request.ActionType_ACTION_TYPE_TRANSFER:
			actionType = 1
		default:
			actionType = 0
		}
		asn1Actions[i] = typedAction{
			Type: actionType,
			Data: action.Raw,
		}
	}

	requestBytes, err := asn1.Marshal(tokenRequestForSigning{
		Actions: asn1Actions,
	})
	require.NoError(t, err)

	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	expectedResult, err := asn1.Marshal(signatureMessage{
		Request: requestBytes,
		Anchor:  anchor,
	})
	require.NoError(t, err)

	// V1 should produce identical output
	assert.Equal(t, expectedResult, v1Result, "V1 should produce ASN.1-compatible output")
}

// TestFastMarshalRoundTrip verifies that fast-marshalled data can be unmarshalled correctly
func TestFastMarshalRoundTrip(t *testing.T) {
	actions := []*TypedAction{
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
	}

	// Fast marshal
	marshalled, err := fastMarshalTokenRequestForSigning(actions)
	require.NoError(t, err)

	// Unmarshal using standard ASN.1
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}
	var unmarshalled tokenRequestForSigning
	_, err = asn1.Unmarshal(marshalled, &unmarshalled)
	require.NoError(t, err)

	// Verify data integrity
	require.Len(t, actions, len(unmarshalled.Actions))
	for i, action := range actions {
		var expectedType int
		switch action.Type {
		case request.ActionType_ACTION_TYPE_ISSUE:
			expectedType = 0
		case request.ActionType_ACTION_TYPE_TRANSFER:
			expectedType = 1
		}
		assert.Equal(t, expectedType, unmarshalled.Actions[i].Type)
		assert.Equal(t, action.Raw, unmarshalled.Actions[i].Data)
	}
}

// TestFastMarshalSignatureMessageRoundTrip verifies round-trip compatibility
func TestFastMarshalSignatureMessageRoundTrip(t *testing.T) {
	request := []byte("request-data")
	anchor := []byte("anchor-data")

	// Fast marshal
	marshalled, err := fastMarshalSignatureMessageV1(request, anchor)
	require.NoError(t, err)

	// Unmarshal using standard ASN.1
	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	var unmarshalled signatureMessage
	_, err = asn1.Unmarshal(marshalled, &unmarshalled)
	require.NoError(t, err)

	// Verify data integrity
	assert.Equal(t, request, unmarshalled.Request)
	assert.Equal(t, anchor, unmarshalled.Anchor)
}

// TestFastMarshalDeterministic verifies that fast marshaller is deterministic
func TestFastMarshalDeterministic(t *testing.T) {
	actions := []*TypedAction{
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
	}

	// Marshal multiple times
	result1, err := fastMarshalTokenRequestForSigning(actions)
	require.NoError(t, err)

	result2, err := fastMarshalTokenRequestForSigning(actions)
	require.NoError(t, err)

	result3, err := fastMarshalTokenRequestForSigning(actions)
	require.NoError(t, err)

	// All results must be identical
	assert.True(t, bytes.Equal(result1, result2))
	assert.True(t, bytes.Equal(result2, result3))
}
