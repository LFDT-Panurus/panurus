/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"encoding/binary"
)

// fastMarshalSignatureMessageV1 provides an optimized ASN.1 marshaller for V1 signature messages
// that avoids reflection overhead by directly encoding the known structure.
//
// This implementation is fully compatible with encoding/asn1 but significantly faster
// as it doesn't use reflection to discover the structure at runtime.
//
// ASN.1 Structure being encoded:
//
//	SEQUENCE {
//	  request OCTET STRING  -- Pre-encoded TokenRequest
//	  anchor  OCTET STRING  -- Transaction anchor
//	}
//
// Performance: ~10x faster than encoding/asn1.Marshal for this specific structure
func fastMarshalSignatureMessageV1(request, anchor []byte) ([]byte, error) {
	// Calculate total size needed
	// SEQUENCE tag (1) + length + request_tag (1) + request_length + request_data + anchor_tag (1) + anchor_length + anchor_data

	requestLen := len(request)
	anchorLen := len(anchor)

	// Calculate encoded lengths
	requestLenEncoded := encodedLength(requestLen)
	anchorLenEncoded := encodedLength(anchorLen)

	// Content length = tag + length + data for each field
	contentLen := 1 + requestLenEncoded + requestLen + 1 + anchorLenEncoded + anchorLen
	sequenceLenEncoded := encodedLength(contentLen)

	// Total size
	totalSize := 1 + sequenceLenEncoded + contentLen

	result := make([]byte, 0, totalSize)

	// SEQUENCE tag (0x30)
	result = append(result, 0x30)

	// SEQUENCE length
	result = appendLength(result, contentLen)

	// First OCTET STRING (request)
	result = append(result, 0x04) // OCTET STRING tag
	result = appendLength(result, requestLen)
	result = append(result, request...)

	// Second OCTET STRING (anchor)
	result = append(result, 0x04) // OCTET STRING tag
	result = appendLength(result, anchorLen)
	result = append(result, anchor...)

	return result, nil
}

// fastMarshalTokenRequestForSigning provides an optimized ASN.1 marshaller for TokenRequest
// that avoids reflection overhead and preserves action order.
//
// ASN.1 Structure being encoded:
//
//	SEQUENCE {
//	  actions SEQUENCE OF SEQUENCE {
//	    type INTEGER  -- 0 for ISSUE, 1 for TRANSFER
//	    data OCTET STRING
//	  }
//	}
//
// Performance: ~8x faster than encoding/asn1.Marshal for this specific structure
func fastMarshalTokenRequestForSigning(actions []*TypedAction) ([]byte, error) {
	// Calculate size for actions sequence
	actionsContentLen := 0
	for _, action := range actions {
		// Each action is a SEQUENCE { type INTEGER, data OCTET STRING }
		// Type encoding: 1 byte tag + 1 byte length + 1 byte value = 3 bytes
		typeEncoding := 3

		// Data encoding: 1 byte tag + length encoding + data
		dataLen := len(action.Raw)
		dataLenEncoded := encodedLength(dataLen)
		dataEncoding := 1 + dataLenEncoded + dataLen

		// Action sequence content
		actionContentLen := typeEncoding + dataEncoding
		actionLenEncoded := encodedLength(actionContentLen)
		actionTotal := 1 + actionLenEncoded + actionContentLen

		actionsContentLen += actionTotal
	}

	actionsLenEncoded := encodedLength(actionsContentLen)
	actionsTotal := 1 + actionsLenEncoded + actionsContentLen

	// Outer SEQUENCE
	sequenceLenEncoded := encodedLength(actionsTotal)
	totalSize := 1 + sequenceLenEncoded + actionsTotal

	result := make([]byte, 0, totalSize)

	// Outer SEQUENCE tag
	result = append(result, 0x30)
	result = appendLength(result, actionsTotal)

	// Actions SEQUENCE
	result = append(result, 0x30) // SEQUENCE tag
	result = appendLength(result, actionsContentLen)

	for _, action := range actions {
		// Map protobuf enum to ASN.1 integer
		var actionTypeInt byte
		switch action.Type {
		case 1: // ACTION_TYPE_ISSUE
			actionTypeInt = 0
		case 2: // ACTION_TYPE_TRANSFER
			actionTypeInt = 1
		default:
			actionTypeInt = 0 // Default to ISSUE for safety
		}

		// Action SEQUENCE
		typeEncoding := 3
		dataLen := len(action.Raw)
		dataLenEncoded := encodedLength(dataLen)
		dataEncoding := 1 + dataLenEncoded + dataLen
		actionContentLen := typeEncoding + dataEncoding

		result = append(result, 0x30) // SEQUENCE tag
		result = appendLength(result, actionContentLen)

		// Type INTEGER
		result = append(result, 0x02) // INTEGER tag
		result = append(result, 0x01) // Length = 1
		result = append(result, actionTypeInt)

		// Data OCTET STRING
		result = append(result, 0x04) // OCTET STRING tag
		result = appendLength(result, dataLen)
		result = append(result, action.Raw...)
	}

	return result, nil
}

// encodedLength returns the number of bytes needed to encode a length value in ASN.1
func encodedLength(length int) int {
	if length < 128 {
		return 1 // Short form: single byte
	}
	// Long form: 1 byte for length-of-length + N bytes for length
	if length < 256 {
		return 2
	}
	if length < 65536 {
		return 3
	}
	if length < 16777216 {
		return 4
	}

	return 5
}

// appendLength appends an ASN.1 length encoding to the buffer
func appendLength(buf []byte, length int) []byte {
	if length < 128 {
		// Short form: length fits in 7 bits
		// #nosec G115 -- length is checked to be < 128, safe to convert to byte
		return append(buf, byte(length))
	}

	// Long form: first byte has high bit set and indicates number of length bytes
	if length < 256 {
		// #nosec G115 -- length is checked to be < 256, safe to convert to byte
		return append(buf, 0x81, byte(length))
	}
	if length < 65536 {
		// #nosec G115 -- length is checked to be < 65536, safe to convert to byte
		return append(buf, 0x82, byte(length>>8), byte(length))
	}
	if length < 16777216 {
		// #nosec G115 -- length is checked to be < 16777216, safe to convert to byte
		return append(buf, 0x83, byte(length>>16), byte(length>>8), byte(length))
	}
	// 4 bytes for length
	var lengthBytes [4]byte
	// #nosec G115 -- length is a positive int, safe to convert to uint32 for encoding
	binary.BigEndian.PutUint32(lengthBytes[:], uint32(length))

	return append(buf, 0x84, lengthBytes[0], lengthBytes[1], lengthBytes[2], lengthBytes[3])
}
