/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackend(t *testing.T) {
	logger := &logging.MockLogger{}
	message := []byte("test-message")
	sigs := [][]byte{[]byte("sig1"), []byte("sig2")}

	ledgerCallCount := 0
	ledger := func(id token.ID) ([]byte, error) {
		ledgerCallCount++
		if id.TxId == "error" {
			return nil, errors.New("ledger error")
		}

		return []byte("state-" + id.TxId), nil
	}

	b := NewBackend(logger, ledger, message, sigs)
	assert.NotNil(t, b)
	assert.Equal(t, sigs, b.Signatures())

	t.Run("HasBeenSignedBy_Success", func(t *testing.T) {
		ctx := context.Background()
		id := driver.Identity("id1")
		verifier := &mock.Verifier{}
		verifier.VerifyReturns(nil)

		sigma, err := b.HasBeenSignedBy(ctx, id, verifier)
		require.NoError(t, err)
		assert.Equal(t, []byte("sig1"), sigma)
		assert.Equal(t, 1, b.Cursor)

		assert.Equal(t, 1, verifier.VerifyCallCount())
		m, s := verifier.VerifyArgsForCall(0)
		assert.Equal(t, message, m)
		assert.Equal(t, []byte("sig1"), s)
	})

	t.Run("HasBeenSignedBy_VerifyError", func(t *testing.T) {
		ctx := context.Background()
		id := driver.Identity("id2")
		verifier := &mock.Verifier{}
		verifier.VerifyReturns(errors.New("invalid signature"))

		sigma, err := b.HasBeenSignedBy(ctx, id, verifier)
		require.Error(t, err)
		assert.Equal(t, "invalid signature", err.Error())
		assert.Equal(t, []byte("sig2"), sigma)
		assert.Equal(t, 2, b.Cursor)
	})

	t.Run("HasBeenSignedBy_InsufficientSignatures", func(t *testing.T) {
		ctx := context.Background()
		id := driver.Identity("id3")
		verifier := &mock.Verifier{}

		sigma, err := b.HasBeenSignedBy(ctx, id, verifier)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient number of signatures")
		assert.Nil(t, sigma)
	})

	t.Run("GetState", func(t *testing.T) {
		res, err := b.GetState(token.ID{TxId: "tx1"})
		require.NoError(t, err)
		assert.Equal(t, []byte("state-tx1"), res)
		assert.Equal(t, 1, ledgerCallCount)

		res, err = b.GetState(token.ID{TxId: "error"})
		require.Error(t, err)
		assert.Equal(t, "ledger error", err.Error())
		assert.Equal(t, 2, ledgerCallCount)
		assert.Nil(t, res)
	})
}
