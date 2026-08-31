/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	ihtlc "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

const maxFuzzHTLCBytes = 64 << 10

// fuzzScript is the reference HTLC script the seed corpus is built from.
func fuzzScript() *htlc.Script {
	return &htlc.Script{
		Sender:    []byte("sender"),
		Recipient: []byte("recipient"),
		Deadline:  time.Unix(1_700_000_000, 0).UTC(),
		HashInfo: htlc.HashInfo{
			Hash:         []byte("hash"),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		},
	}
}

// FuzzVerifyOwnerNoPanic hunts for owner bytes that panic VerifyOwner instead of returning an error.
// senderRawOwner is the raw owner field of a token being spent, so it is fully attacker-controlled
// and reaches both the TypedIdentity unmarshaller and the JSON decode of the embedded htlc.Script
// ahead of any signature check.
func FuzzVerifyOwnerNoPanic(f *testing.F) {
	scriptRaw, err := json.Marshal(fuzzScript())
	require.NoError(f, err)
	typed, err := identity.WrapWithType(ihtlc.ScriptType, scriptRaw)
	require.NoError(f, err)

	seeds := [][]byte{
		nil,
		{},
		[]byte("not a typed identity"),
		typed,
		scriptRaw,
	}
	// A typed identity of the right type wrapping garbage, and one of the wrong type.
	if wrapped, err := identity.WrapWithType(ihtlc.ScriptType, []byte("{")); err == nil {
		seeds = append(seeds, wrapped)
	}
	if wrapped, err := identity.WrapWithType(ihtlc.ScriptType+1, scriptRaw); err == nil {
		seeds = append(seeds, wrapped)
	}
	for _, seed := range seeds {
		f.Add(seed, []byte("recipient"))
	}

	f.Fuzz(func(t *testing.T, sender, out []byte) {
		if len(sender) > maxFuzzHTLCBytes || len(out) > maxFuzzHTLCBytes {
			t.Skip("input beyond the size any owner field can reach in practice")
		}
		require.NotPanics(t, func() {
			// Both sides of the deadline, so the claim and the reclaim branch are both reached.
			_, _, _ = ihtlc.VerifyOwner(sender, out, time.Unix(1, 0).UTC())
			_, _, _ = ihtlc.VerifyOwner(sender, out, time.Unix(2_000_000_000, 0).UTC())
		})
	})
}

// FuzzMetadataClaimKeyCheckNoPanic hunts for claim signature bytes that panic MetadataClaimKeyCheck.
// sig is the signature an unlocking party supplies, decoded into an htlc.ClaimSignature before its
// preimage is ever checked against the action metadata.
func FuzzMetadataClaimKeyCheckNoPanic(f *testing.F) {
	script := fuzzScript()
	preimage := []byte("preimage")
	claim, err := json.Marshal(&htlc.ClaimSignature{RecipientSignature: []byte("sig"), Preimage: preimage})
	require.NoError(f, err)

	for _, seed := range [][]byte{
		nil,
		{},
		[]byte("{"),
		[]byte(`{}`),
		[]byte(`{"Preimage":null,"RecipientSignature":null}`),
		[]byte(`{"Preimage":"cHJl"}`),
		[]byte(`{"Preimage":"cHJl","RecipientSignature":"c2ln","NotAField":1}`),
		claim,
	} {
		f.Add(seed, preimage)
	}

	f.Fuzz(func(t *testing.T, sig, metadataValue []byte) {
		if len(sig) > maxFuzzHTLCBytes || len(metadataValue) > maxFuzzHTLCBytes {
			t.Skip("input beyond the size any claim signature can reach in practice")
		}
		image, err := script.HashInfo.Image(preimage)
		require.NoError(t, err)

		action := &mock.Action{}
		action.GetMetadataReturns(map[string][]byte{htlc.ClaimKey(image): metadataValue})

		require.NotPanics(t, func() {
			_, _ = ihtlc.MetadataClaimKeyCheck(action, script, ihtlc.Claim, sig)
			_, _ = ihtlc.MetadataClaimKeyCheck(action, script, ihtlc.Reclaim, sig)
		})
	})
}
