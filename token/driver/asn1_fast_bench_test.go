/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
)

// BenchmarkFastMarshalTokenRequestForSigning_Small benchmarks fast marshaller with small data
func BenchmarkFastMarshalTokenRequestForSigning_Small(b *testing.B) {
	actions := []*TypedAction{
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalTokenRequestForSigning(actions)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalTokenRequestForSigning_Small benchmarks standard ASN.1 with small data
func BenchmarkStdMarshalTokenRequestForSigning_Small(b *testing.B) {
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}
	req := tokenRequestForSigning{
		Actions: []typedAction{
			{Type: 0, Data: []byte("issue1")},
			{Type: 1, Data: []byte("transfer1")},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastMarshalTokenRequestForSigning_Medium benchmarks fast marshaller with medium data
func BenchmarkFastMarshalTokenRequestForSigning_Medium(b *testing.B) {
	actions := []*TypedAction{
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 100)},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 200)},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 150)},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 180)},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 220)},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalTokenRequestForSigning(actions)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalTokenRequestForSigning_Medium benchmarks standard ASN.1 with medium data
func BenchmarkStdMarshalTokenRequestForSigning_Medium(b *testing.B) {
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}
	req := tokenRequestForSigning{
		Actions: []typedAction{
			{Type: 0, Data: make([]byte, 100)},
			{Type: 0, Data: make([]byte, 200)},
			{Type: 0, Data: make([]byte, 150)},
			{Type: 1, Data: make([]byte, 180)},
			{Type: 1, Data: make([]byte, 220)},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastMarshalTokenRequestForSigning_Large benchmarks fast marshaller with large data
func BenchmarkFastMarshalTokenRequestForSigning_Large(b *testing.B) {
	actions := []*TypedAction{
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 5000)},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 8000)},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 6000)},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 7000)},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 4000)},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 9000)},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 5500)},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalTokenRequestForSigning(actions)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalTokenRequestForSigning_Large benchmarks standard ASN.1 with large data
func BenchmarkStdMarshalTokenRequestForSigning_Large(b *testing.B) {
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}
	req := tokenRequestForSigning{
		Actions: []typedAction{
			{Type: 0, Data: make([]byte, 5000)},
			{Type: 0, Data: make([]byte, 8000)},
			{Type: 0, Data: make([]byte, 6000)},
			{Type: 0, Data: make([]byte, 7000)},
			{Type: 1, Data: make([]byte, 4000)},
			{Type: 1, Data: make([]byte, 9000)},
			{Type: 1, Data: make([]byte, 5500)},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastMarshalSignatureMessageV1_Small benchmarks fast marshaller with small signature message
func BenchmarkFastMarshalSignatureMessageV1_Small(b *testing.B) {
	request := []byte("small-request-data")
	anchor := []byte("anchor")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalSignatureMessageV1(request, anchor)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalSignatureMessageV1_Small benchmarks standard ASN.1 with small signature message
func BenchmarkStdMarshalSignatureMessageV1_Small(b *testing.B) {
	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	msg := signatureMessage{
		Request: []byte("small-request-data"),
		Anchor:  []byte("anchor"),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastMarshalSignatureMessageV1_Medium benchmarks fast marshaller with medium signature message
func BenchmarkFastMarshalSignatureMessageV1_Medium(b *testing.B) {
	request := make([]byte, 1000)
	anchor := make([]byte, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalSignatureMessageV1(request, anchor)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalSignatureMessageV1_Medium benchmarks standard ASN.1 with medium signature message
func BenchmarkStdMarshalSignatureMessageV1_Medium(b *testing.B) {
	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	msg := signatureMessage{
		Request: make([]byte, 1000),
		Anchor:  make([]byte, 64),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastMarshalSignatureMessageV1_Large benchmarks fast marshaller with large signature message
func BenchmarkFastMarshalSignatureMessageV1_Large(b *testing.B) {
	request := make([]byte, 50000)
	anchor := make([]byte, 128)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalSignatureMessageV1(request, anchor)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStdMarshalSignatureMessageV1_Large benchmarks standard ASN.1 with large signature message
func BenchmarkStdMarshalSignatureMessageV1_Large(b *testing.B) {
	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	msg := signatureMessage{
		Request: make([]byte, 50000),
		Anchor:  make([]byte, 128),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshalToMessageToSignV1_Complete benchmarks the complete V1 marshalling flow
func BenchmarkMarshalToMessageToSignV1_Complete(b *testing.B) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 500)},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: make([]byte, 800)},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, 600)},
		},
	}
	anchor := []byte("test-anchor-data")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := tr.marshalToMessageToSignV1(anchor)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkManySmallItems benchmarks performance with many small items
func BenchmarkManySmallItems_Fast(b *testing.B) {
	actions := make([]*TypedAction, 100)
	for i := range actions {
		actionType := request.ActionType_ACTION_TYPE_ISSUE
		if i >= 50 {
			actionType = request.ActionType_ACTION_TYPE_TRANSFER
		}
		actions[i] = &TypedAction{
			Type: actionType,
			Raw:  []byte{byte(i)},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := fastMarshalTokenRequestForSigning(actions)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkManySmallItems_Std benchmarks standard ASN.1 with many small items
func BenchmarkManySmallItems_Std(b *testing.B) {
	type typedAction struct {
		Type int
		Data []byte
	}
	type tokenRequestForSigning struct {
		Actions []typedAction
	}
	asn1Actions := make([]typedAction, 100)
	for i := range asn1Actions {
		actionType := 0 // ISSUE
		if i >= 50 {
			actionType = 1 // TRANSFER
		}
		asn1Actions[i] = typedAction{
			Type: actionType,
			Data: []byte{byte(i)},
		}
	}
	req := tokenRequestForSigning{
		Actions: asn1Actions,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := asn1.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
	}
}
