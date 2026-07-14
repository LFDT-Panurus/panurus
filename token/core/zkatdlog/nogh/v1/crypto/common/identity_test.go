/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/require"
)

func TestWrappedSigningIdentity(t *testing.T) {
	id := driver.Identity("alice")
	signer := &mock.Signer{}
	w := &WrappedSigningIdentity{
		Identity: id,
		Signer:   signer,
	}

	// Test Serialize
	serialized, err := w.Serialize()
	require.NoError(t, err)
	require.Equal(t, []byte(id), serialized)

	// Test Sign
	raw := []byte("hello")
	sig := []byte("signature")
	signer.SignReturns(sig, nil)
	signed, err := w.Sign(raw)
	require.NoError(t, err)
	require.Equal(t, sig, signed)
	require.Equal(t, 1, signer.SignCallCount())
	require.Equal(t, raw, signer.SignArgsForCall(0))

	// Test Sign with nil signer
	wNil := &WrappedSigningIdentity{
		Identity: id,
		Signer:   nil,
	}
	_, err = wNil.Sign(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "please initialize signing identity in WrappedSigningIdentity")
}
