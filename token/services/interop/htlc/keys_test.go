/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"encoding/hex"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

func TestClaimKey(t *testing.T) {
	v := []byte{0x01, 0x02, 0x03}
	require.Equal(t, htlc.ClaimPreImage+hex.EncodeToString(v), htlc.ClaimKey(v))
}

func TestLockKey(t *testing.T) {
	v := []byte{0xAB, 0xCD}
	require.Equal(t, htlc.LockHash+hex.EncodeToString(v), htlc.LockKey(v))
}

func TestLockValue(t *testing.T) {
	v := []byte{0xDE, 0xAD}
	require.Equal(t, []byte(hex.EncodeToString(v)), htlc.LockValue(v))
}

func TestClaimKeyEmpty(t *testing.T) {
	require.Equal(t, htlc.ClaimPreImage, htlc.ClaimKey(nil))
}

func TestLockKeyEmpty(t *testing.T) {
	require.Equal(t, htlc.LockHash, htlc.LockKey(nil))
}

func TestClaimAndLockKeysDiffer(t *testing.T) {
	v := []byte{0x01}
	require.NotEqual(t, htlc.ClaimKey(v), htlc.LockKey(v))
}

func TestLockValueRoundtrip(t *testing.T) {
	v := []byte("somedata")
	decoded, err := hex.DecodeString(string(htlc.LockValue(v)))
	require.NoError(t, err)
	require.Equal(t, v, decoded)
}
