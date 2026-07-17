/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func endorsementTransaction() *Transaction {
	return &Transaction{Payload: &Payload{
		TxID:         network.TxID{Nonce: []byte("nonce"), Creator: []byte("creator")},
		ID:           "anchor",
		tmsID:        token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"},
		Signer:       []byte("signer"),
		Transient:    network.TransientMap{"key": []byte("value")},
		TokenRequest: token.NewRequest(nil, "anchor"),
	}}
}

func cloneEndorsementTransaction(tx *Transaction) *Transaction {
	payload := *tx.Payload
	payload.TxID.Nonce = append([]byte(nil), tx.TxID.Nonce...)
	payload.TxID.Creator = append([]byte(nil), tx.TxID.Creator...)
	payload.Signer = append([]byte(nil), tx.Signer...)
	payload.Transient = network.TransientMap{}
	for key, value := range tx.Transient {
		payload.Transient[key] = append([]byte(nil), value...)
	}

	return &Transaction{Payload: &payload}
}

func TestValidateEndorsedTransactionRejectsImmutableFieldSubstitution(t *testing.T) {
	expected := endorsementTransaction()
	require.NoError(t, validateEndorsedTransaction(expected, cloneEndorsementTransaction(expected)))

	tests := []struct {
		name   string
		mutate func(*Transaction)
		match  string
	}{
		{name: "network signer", mutate: func(tx *Transaction) { tx.Signer = []byte("mallory") }, match: "network signer changed"},
		{name: "network creator", mutate: func(tx *Transaction) { tx.TxID.Creator = []byte("mallory") }, match: "network transaction identity changed"},
		{name: "network nonce", mutate: func(tx *Transaction) { tx.TxID.Nonce = []byte("mallory") }, match: "network transaction identity changed"},
		{name: "transient value", mutate: func(tx *Transaction) { tx.Transient["key"] = []byte("malicious") }, match: "transient data changed"},
		{name: "transient key", mutate: func(tx *Transaction) { tx.Transient["extra"] = []byte("malicious") }, match: "transient data changed"},
		{name: "tms", mutate: func(tx *Transaction) { tx.tmsID.Namespace = "other" }, match: "transaction identity changed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := cloneEndorsementTransaction(expected)
			test.mutate(received)
			err := validateEndorsedTransaction(expected, received)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.match)
		})
	}
}
