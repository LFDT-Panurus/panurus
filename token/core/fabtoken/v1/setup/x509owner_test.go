/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"encoding/hex"
	"testing"

	fabpp "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/pp"
	x509crypto "github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPP(t *testing.T) *PublicParams {
	t.Helper()
	pp, err := Setup(8)
	require.NoError(t, err)
	pp.AddAuditor([]byte("a"))
	pp.AddIssuer([]byte("i"))
	pp.ExtraData = map[string][]byte{"k": []byte("v")}

	return pp
}

func TestPublicParams_X509OwnerCertificatesRoundTrip(t *testing.T) {
	root := x509crypto.NewTestCertificate(t, x509crypto.NewTestCATemplate("root"), nil)
	intermediate := x509crypto.NewTestCertificate(t, x509crypto.NewTestCATemplate("int"), root)

	pp := newTestPP(t)
	pp.X509OwnerRoots = [][]byte{root.PEM}
	pp.X509OwnerIntermediates = [][]byte{intermediate.PEM}
	require.NoError(t, pp.Validate())

	raw, err := pp.Serialize()
	require.NoError(t, err)
	got, err := NewPublicParamsFromBytes(raw, FabTokenDriverName, ProtocolV1)
	require.NoError(t, err)
	assert.Equal(t, pp.X509OwnerRoots, got.X509OwnerRoots)
	assert.Equal(t, pp.X509OwnerIntermediates, got.X509OwnerIntermediates)
	require.NoError(t, got.Validate())

	// roots only
	pp.X509OwnerIntermediates = nil
	raw, err = pp.Bytes()
	require.NoError(t, err)
	got = &PublicParams{}
	require.NoError(t, got.FromBytes(raw))
	assert.Equal(t, pp.X509OwnerRoots, got.X509OwnerRoots)
	assert.Nil(t, got.X509OwnerIntermediates)
}

func TestPublicParams_X509OwnerCertificatesAbsent(t *testing.T) {
	pp := newTestPP(t)
	raw, err := pp.Bytes()
	require.NoError(t, err)

	msg := &fabpp.PublicParameters{}
	require.NoError(t, proto.Unmarshal(raw, msg))
	assert.Nil(t, msg.X509OwnerCertificates)

	// FromBytes resets fields previously set.
	got := &PublicParams{X509OwnerRoots: [][]byte{[]byte("x")}, X509OwnerIntermediates: [][]byte{[]byte("y")}}
	require.NoError(t, got.FromBytes(raw))
	assert.Nil(t, got.X509OwnerRoots)
	assert.Nil(t, got.X509OwnerIntermediates)

	// Empty, non-nil lists are not encoded.
	pp.X509OwnerRoots = [][]byte{}
	pp.X509OwnerIntermediates = [][]byte{}
	raw2, err := pp.Bytes()
	require.NoError(t, err)
	assert.Equal(t, raw, raw2)
}

// TestPublicParams_BytesWithoutX509OwnerCertificatesUnchanged pins the encoding of a PP
// without owner CA certificates to the encoding produced before field 8 existed.
func TestPublicParams_BytesWithoutX509OwnerCertificatesUnchanged(t *testing.T) {
	raw, err := newTestPP(t).Bytes()
	require.NoError(t, err)
	expected := "0a0866616274" + "6f6b656e" + // 1: token_driver_name "fabtoken"
		"1001" + // 2: token_driver_version 1
		"1a030a0161" + // 3: auditor {raw: "a"}
		"22030a0169" + // 4: issuers [{raw: "i"}]
		"28ff01" + // 5: max_token 255
		"3008" + // 6: quantity_precision 8
		"3a060a016b120176" // 7: metadata {"k": "v"}
	assert.Equal(t, expected, hex.EncodeToString(raw))
}

func TestPublicParams_ValidateX509OwnerCertificates(t *testing.T) {
	root := x509crypto.NewTestCertificate(t, x509crypto.NewTestCATemplate("root"), nil)
	foreign := x509crypto.NewTestCertificate(t, x509crypto.NewTestCATemplate("foreign"), nil)
	foreignInt := x509crypto.NewTestCertificate(t, x509crypto.NewTestCATemplate("foreign-int"), foreign)

	tests := []struct {
		name          string
		roots         [][]byte
		intermediates [][]byte
		err           string
	}{
		{name: "none"},
		{name: "valid root", roots: [][]byte{root.PEM}},
		{name: "intermediates without roots", intermediates: [][]byte{foreignInt.PEM}, err: "intermediates set without roots"},
		{name: "non PEM root", roots: [][]byte{[]byte("garbage")}, err: "expected exactly one PEM block"},
		{name: "intermediate not chaining", roots: [][]byte{root.PEM}, intermediates: [][]byte{foreignInt.PEM}, err: "does not chain to a root"},
		{name: "duplicate root", roots: [][]byte{root.PEM, root.PEM}, err: "duplicate x509 owner CA certificate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pp := newTestPP(t)
			pp.X509OwnerRoots = tc.roots
			pp.X509OwnerIntermediates = tc.intermediates
			err := pp.Validate()
			if tc.err == "" {
				require.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid x509 owner certificates")
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
