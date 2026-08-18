/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb

// Tests for the non-vacuity handling in GetTransactionEndorsementAcks. A row
// that carries no endorser or no signature is dropped from the result, so that
// one such row does not make the whole transaction — its valid acks and its
// token request — unreadable.

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// endorsementAcksStub answers GetTransactionEndorsementAcks with a configured
// map, and inherits the panicking implementations for the rest of the interface.
type endorsementAcksStub struct {
	*getTokenRequestsStub
	acks map[string][]byte
}

func (s *endorsementAcksStub) GetTransactionEndorsementAcks(_ context.Context, _ string) (map[string][]byte, error) {
	return s.acks, nil
}

// TestGetTransactionEndorsementAcks_VacuousRowsDropped verifies that a row with
// an empty endorser or an empty signature is removed from the returned map while
// the valid rows are still returned.
func TestGetTransactionEndorsementAcks_VacuousRowsDropped(t *testing.T) {
	alice := token.Identity("alice").UniqueID()
	svc, err := newStoreService(&endorsementAcksStub{
		getTokenRequestsStub: &getTokenRequestsStub{},
		acks: map[string][]byte{
			alice:                            []byte("sigma"),
			token.Identity(nil).UniqueID():   []byte("sigma"), // empty endorser
			token.Identity("bob").UniqueID(): nil,             // empty signature
		},
	})
	require.NoError(t, err)

	acks, err := svc.GetTransactionEndorsementAcks(context.Background(), "tx1")
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{alice: []byte("sigma")}, acks)
}
