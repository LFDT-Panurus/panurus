/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"

	_ "crypto/sha256"
)

func htlcOwner(t *testing.T, hash []byte) []byte {
	t.Helper()
	script := &htlc.Script{
		Sender:    []byte("sender"),
		Recipient: []byte("recipient"),
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  htlc.HashInfo{Hash: hash, HashFunc: crypto.SHA256},
	}
	raw, err := json.Marshal(script)
	require.NoError(t, err)
	owner, err := identity.WrapWithType(htlc.ScriptType, raw)
	require.NoError(t, err)

	return owner
}

func TestTransferHTLCValidate_Lock(t *testing.T) {
	t.Parallel()

	hash := []byte("0123456789abcdef0123456789abcdef")
	owner := htlcOwner(t, hash)
	outputs := []token.OutputDescription{{Recipient: owner}}

	// lock without the lock metadata is rejected
	ctx := &validator.Context{TransferAction: &token.TransferAction{Outputs: outputs}}
	require.Error(t, validator.TransferHTLCValidate(context.Background(), ctx))

	// lock with the right metadata is accepted and the key counted
	key := htlc.LockKey(hash)
	ctx = &validator.Context{TransferAction: &token.TransferAction{
		Outputs:  outputs,
		Metadata: map[string][]byte{key: htlc.LockValue(hash)},
	}}
	require.NoError(t, validator.TransferHTLCValidate(context.Background(), ctx))
	require.Equal(t, map[string]int{key: 1}, ctx.MetadataCounter)

	// lock with a wrong metadata value is rejected
	ctx = &validator.Context{TransferAction: &token.TransferAction{
		Outputs:  outputs,
		Metadata: map[string][]byte{key: []byte("wrong")},
	}}
	require.Error(t, validator.TransferHTLCValidate(context.Background(), ctx))
}

func TestTransferHTLCValidate_HTLCInputMustBeOneToOne(t *testing.T) {
	t.Parallel()

	owner := htlcOwner(t, []byte("0123456789abcdef0123456789abcdef"))
	plain, err := identity.WrapWithType(1, []byte("alice"))
	require.NoError(t, err)

	ctx := &validator.Context{
		TransferAction: &token.TransferAction{Outputs: []token.OutputDescription{{Recipient: plain}, {Recipient: plain}}},
		InputTokens:    []*token.Input{{Owner: owner}},
	}
	require.ErrorIs(t, validator.TransferHTLCValidate(context.Background(), ctx), validator.ErrInvalidHTLCAction)

	// nil input token must error rather than panic
	ctx = &validator.Context{TransferAction: &token.TransferAction{}, InputTokens: []*token.Input{nil}}
	require.Error(t, validator.TransferHTLCValidate(context.Background(), ctx))
}

func TestTransferHTLCValidate_NonHTLCIsIgnored(t *testing.T) {
	t.Parallel()

	plain, err := identity.WrapWithType(1, []byte("alice"))
	require.NoError(t, err)
	ctx := &validator.Context{
		TransferAction: &token.TransferAction{Outputs: []token.OutputDescription{{Recipient: plain}, {}}},
		InputTokens:    []*token.Input{{Owner: plain}},
	}
	require.NoError(t, validator.TransferHTLCValidate(context.Background(), ctx))
	require.Empty(t, ctx.MetadataCounter)
}
