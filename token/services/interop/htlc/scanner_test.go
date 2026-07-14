/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"bytes"
	"crypto"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// TestWithMessagefNilReturnsBug documents the root cause fixed in ScanForPreImage.
// errors.WithMessagef(nil, ...) returns nil instead of an error. Before the fix,
// scanner.go line 85 passed the already-nil err variable to WithMessagef after the
// Image() call succeeded, so a ledger hash mismatch silently returned (nil, nil).
func TestWithMessagefNilReturnsBug(t *testing.T) {
	require.NoError(t, errors.WithMessagef(nil, "wrapping nil must return nil: %s", "confirmed"))
	require.Error(t, errors.Errorf("Errorf always produces a non-nil error: %x != %x", []byte("a"), []byte("b")))
}

// TestHashInfoImageMismatch confirms that a wrong preimage hashes to a different
// image — the scenario that ScanForPreImage now correctly rejects after replacing
// errors.WithMessagef(err, ...) with errors.Errorf(...) at scanner.go line 85.
func TestHashInfoImageMismatch(t *testing.T) {
	hi := &htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.Base64}

	correctImage, err := hi.Image([]byte("correct-preimage"))
	require.NoError(t, err)
	require.NotEmpty(t, correctImage)

	wrongImage, err := hi.Image([]byte("wrong-preimage"))
	require.NoError(t, err)

	// A wrong preimage produces a different hash: bytes.Equal returns false.
	// Before the fix, ScanForPreImage would return (nil, nil) here because
	// errors.WithMessagef(nil, ...) == nil. After the fix, it returns (nil, error).
	require.False(t, bytes.Equal(correctImage, wrongImage))
}
