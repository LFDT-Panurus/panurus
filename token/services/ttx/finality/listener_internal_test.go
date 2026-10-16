/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"encoding/base64"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/require"
)

// TestCheckTokenRequest exercises checkTokenRequest itself - the guard that refuses to
// process the tokens of a transaction whose token request does not hash to the reference
// the ledger carries. It lives in an internal test because the method is unexported: an
// external test can only re-implement the hash comparison, which asserts on the standard
// library rather than on this package.
func TestCheckTokenRequest(t *testing.T) {
	trToSign := []byte("the token request as signed")
	matching, err := base64.StdEncoding.DecodeString(utils.Hashable(trToSign).String())
	require.NoError(t, err)

	tests := []struct {
		name      string
		trToSign  []byte
		reference []byte
		wantErr   bool
	}{
		{
			name:      "the reference is the hash of the request",
			trToSign:  trToSign,
			reference: matching,
		},
		{
			name:      "the reference is the hash of a different request",
			trToSign:  trToSign,
			reference: utils.Hashable([]byte("a different token request")).Raw(),
			wantErr:   true,
		},
		{
			name:      "no reference at all",
			trToSign:  trToSign,
			reference: nil,
			wantErr:   true,
		},
		{
			name: "the reference is the request itself, not its hash",
			// The guard's whole point: a caller passing the payload where the digest
			// belongs must be rejected, not silently accepted.
			trToSign:  trToSign,
			reference: trToSign,
			wantErr:   true,
		},
		{
			name:      "an empty request still has a hash, and it must match",
			trToSign:  nil,
			reference: utils.Hashable(nil).Raw(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener := &Listener{logger: logging.MustGetLogger()}
			err := listener.checkTokenRequest("tx-1", test.trToSign, test.reference)
			// The recovery handler carries its own copy of the same guard, so the two must
			// agree on every input: a transaction rejected on the listener path cannot be
			// accepted on the recovery path.
			handler := &TTXRecoveryHandler{logger: logging.MustGetLogger()}
			recoveryErr := handler.checkTokenRequest("tx-1", test.trToSign, test.reference)

			if test.wantErr {
				require.Error(t, err)
				require.ErrorContains(t, err, "token requests do not match")
				require.Error(t, recoveryErr, "the recovery handler's copy of the guard disagrees")

				return
			}
			require.NoError(t, err)
			require.NoError(t, recoveryErr, "the recovery handler's copy of the guard disagrees")
		})
	}
}
