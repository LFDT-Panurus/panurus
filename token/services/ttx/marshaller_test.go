/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/asn1"
	"reflect"
	"strings"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func TestMarshalUnmarshalJSON(t *testing.T) {
	type foo struct {
		A string
		B int
	}
	in := &foo{A: "hello", B: 42}
	b, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var out foo
	if err := Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !reflect.DeepEqual(in, &out) {
		t.Fatalf("expected %+v, got %+v", in, &out)
	}
}

func TestMarshalMetaUnmarshalMeta(t *testing.T) {
	m := map[string][]byte{
		"b": []byte("2"),
		"a": []byte("1"),
	}
	r, err := MarshalMeta(m)
	if err != nil {
		t.Fatalf("MarshalMeta error: %v", err)
	}
	got, err := UnmarshalMeta(r)
	if err != nil {
		t.Fatalf("UnmarshalMeta error: %v", err)
	}
	if !reflect.DeepEqual(m, got) {
		t.Fatalf("expected %+v, got %+v", m, got)
	}
}

func TestTransactionMarshalUnmarshalRoundtrip(t *testing.T) {
	// build a minimal transaction
	tx := &Transaction{
		Payload: &Payload{
			TxID: network.TxID{Nonce: []byte{1, 2, 3}, Creator: []byte{9, 8}},
			ID:   "tx-123",
			// set tmsID in payload
			tmsID:        token.TMSID{Network: "net1", Channel: "ch1", Namespace: "ns1"},
			Signer:       nil,
			Transient:    map[string][]byte{"k": []byte("v")},
			TokenRequest: nil,
			Envelope:     nil,
		},
	}

	// marshal
	raw, err := marshal(tx)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// prepare payload to unmarshal into; provide a non-nil Envelope to avoid calling getNetwork
	p := &Payload{
		Transient:    map[string][]byte{},
		TokenRequest: token.NewRequest(nil, ""),
		Envelope:     &network.Envelope{},
	}

	// call unmarshal with a getNetwork func that should not be invoked
	badGetNetwork := func(network string, channel string) (dep.Network, error) {
		t.Fatalf("getNetwork should not be called")

		return nil, nil
	}

	if err := unmarshal(badGetNetwork, p, raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// verify fields
	if !reflect.DeepEqual(p.TxID.Nonce, tx.TxID.Nonce) {
		t.Fatalf("nonce mismatch: expected %v got %v", tx.TxID.Nonce, p.TxID.Nonce)
	}
	if !reflect.DeepEqual(p.TxID.Creator, tx.TxID.Creator) {
		t.Fatalf("creator mismatch: expected %v got %v", tx.TxID.Creator, p.TxID.Creator)
	}
	if p.ID != tx.ID() {
		t.Fatalf("id mismatch: expected %s got %s", tx.ID(), p.ID)
	}
	if p.tmsID.Network != tx.tmsID.Network || p.tmsID.Channel != tx.tmsID.Channel || p.tmsID.Namespace != tx.tmsID.Namespace {
		t.Fatalf("tmsID mismatch: expected %+v got %+v", tx.tmsID, p.tmsID)
	}
	if !reflect.DeepEqual(p.Transient, tx.Transient) {
		t.Fatalf("transient mismatch: expected %+v got %+v", tx.Transient, p.Transient)
	}
}

func TestUnmarshal_ErrorCases(t *testing.T) {
	// helper to marshal TransactionSer
	m := func(s TransactionSer) []byte {
		b, err := asn1.Marshal(s)
		if err != nil {
			t.Fatalf("failed preparing test data: %v", err)
		}

		return b
	}

	// case: missing network
	{
		ser := TransactionSer{Network: "", Namespace: "ns"}
		raw := m(ser)
		p := &Payload{Transient: map[string][]byte{}, TokenRequest: token.NewRequest(nil, ""), Envelope: &network.Envelope{}}
		if err := unmarshal(func(network, channel string) (dep.Network, error) { return nil, nil }, p, raw); !errors.Is(err, ErrNetworkNotSet) {
			t.Fatalf("expected ErrNetworkNotSet, got %v", err)
		}
	}

	// case: missing namespace
	{
		ser := TransactionSer{Network: "net", Namespace: ""}
		raw := m(ser)
		p := &Payload{Transient: map[string][]byte{}, TokenRequest: token.NewRequest(nil, ""), Envelope: &network.Envelope{}}
		if err := unmarshal(func(network, channel string) (dep.Network, error) { return nil, nil }, p, raw); !errors.Is(err, ErrNamespaceNotSet) {
			t.Fatalf("expected ErrNamespaceNotSet, got %v", err)
		}
	}

	// case: invalid transient raw
	{
		ser := TransactionSer{Network: "net", Namespace: "ns", Transient: []byte{1, 2, 3}}
		raw := m(ser)
		p := &Payload{Transient: map[string][]byte{}, TokenRequest: token.NewRequest(nil, ""), Envelope: &network.Envelope{}}
		err := unmarshal(func(network, channel string) (dep.Network, error) { return nil, nil }, p, raw)
		if err == nil || !strings.Contains(err.Error(), "failed unmarshalling transient") {
			t.Fatalf("expected transient unmarshal error, got %v", err)
		}
	}

	// case: invalid token request raw
	{
		ser := TransactionSer{Network: "net", Namespace: "ns", TokenRequest: []byte{1, 2, 3}}
		raw := m(ser)
		p := &Payload{Transient: map[string][]byte{}, TokenRequest: token.NewRequest(nil, ""), Envelope: &network.Envelope{}}
		err := unmarshal(func(network, channel string) (dep.Network, error) { return nil, nil }, p, raw)
		if err == nil || !strings.Contains(err.Error(), "failed unmarshalling token request") {
			t.Fatalf("expected token request unmarshal error, got %v", err)
		}
	}

	// case: getNetwork returns error
	{
		ser := TransactionSer{Network: "net", Namespace: "ns"}
		raw := m(ser)
		p := &Payload{Transient: map[string][]byte{}, TokenRequest: token.NewRequest(nil, ""), Envelope: nil}
		expErr := errors.New("no network")
		err := unmarshal(func(network, channel string) (dep.Network, error) { return nil, expErr }, p, raw)
		if err == nil || !strings.Contains(err.Error(), "no network") {
			t.Fatalf("expected getNetwork error propagated, got %v", err)
		}
	}
}

func TestMarshal_ErrorCases(t *testing.T) {
	// case: empty network -> ErrNetworkNotSet
	{
		tx := &Transaction{
			Payload: &Payload{
				tmsID: token.TMSID{Network: "", Channel: "ch1", Namespace: "ns1"},
			},
		}
		if _, err := marshal(tx); !errors.Is(err, ErrNetworkNotSet) {
			t.Fatalf("expected ErrNetworkNotSet, got %v", err)
		}
	}

	// case: empty namespace -> ErrNamespaceNotSet
	{
		tx := &Transaction{
			Payload: &Payload{
				tmsID: token.TMSID{Network: "net1", Channel: "ch1", Namespace: ""},
			},
		}
		if _, err := marshal(tx); !errors.Is(err, ErrNamespaceNotSet) {
			t.Fatalf("expected ErrNamespaceNotSet, got %v", err)
		}
	}
}
