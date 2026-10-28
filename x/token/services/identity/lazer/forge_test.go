/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"encoding/asn1"

	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/stretchr/testify/require"
)

// TestForgeryWithoutCredential builds an identity and a signature from the
// trivial witness (zero user state and credential, nyms to zero), using only
// the issuer public key, and checks that verification rejects it.
func TestForgeryWithoutCredential(t *testing.T) {
	_, ipk := NewIssuerKey()
	u, err := anoncreds.NewUser(ipk)
	require.NoError(t, err)
	defer u.Clear()

	usk := make([]byte, anoncreds.AttrLen)
	st := make([]byte, anoncreds.MaxLenUserState())
	st[len(st)-1] = 0x01
	if u.Import(st, zeroAttributes()) != nil {
		st[len(st)-1] = 0x80
		require.NoError(t, u.Import(st, zeroAttributes()))
	}
	cred := make([]byte, (64*8+2*8*64*9)/8+1)
	cred[len(cred)-1] = 0x01

	var coms []*anoncreds.Commitment
	for range idxCom {
		c, err := anoncreds.Commit(usk)
		require.NoError(t, err)
		defer c.Clear()
		coms = append(coms, c)
	}
	_, err = u.PresGen(cred, nil, coms, idxCom, identityContext(ipk))
	require.Error(t, err, "presentation generated from the zero witness")

	// a signature with a valid SoK but without a valid presentation
	pres := make([]byte, 1024)
	msg := []byte("forged message")
	sok, err := coms[0].SokProve(signatureContext(msg), usk)
	require.NoError(t, err)

	id, err := (&SerializedIdentity{NymSK: coms[0].Nym, NymEID: coms[1].Nym, NymRH: coms[2].Nym}).Bytes()
	require.NoError(t, err)
	sig, err := asn1.Marshal(Signature{Pres: pres, Sok: sok})
	require.NoError(t, err)

	d, err := NewDeserializer(ipk)
	require.NoError(t, err)
	defer d.Close()
	v, err := d.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	require.Error(t, v.Verify(msg, sig), "signature forged without a credential was accepted")
}
