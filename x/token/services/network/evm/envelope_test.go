/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

func sampleDelta() *statedelta.StateDelta {
	var anchor, trh, pph, tokenID, marker [32]byte
	anchor[31] = 0xC1
	trh[0] = 0x11
	pph[0] = 0x22
	tokenID[0] = 0x33
	marker[0] = 0x44

	return &statedelta.StateDelta{
		Anchor:              anchor,
		Outputs:             []statedelta.OutputToken{{TokenID: tokenID, SNMarker: marker, TokenData: []byte("token")}},
		TokenRequestHash:    trh,
		PublicParamsHash:    pph,
		PublicParamsVersion: 3,
	}
}

func TestEnvelopeRoundTripsEndorsement(t *testing.T) {
	env := NewApprovedEnvelope("anchorhex", sampleDelta(), [][]byte{{0x01, 0x02}, {0x03, 0x04}})

	raw, err := env.Bytes()
	require.NoError(t, err)

	var got Envelope
	require.NoError(t, got.FromBytes(raw))
	assert.Equal(t, "anchorhex", got.TxID())
	require.NotNil(t, got.Delta)
	assert.Equal(t, sampleDelta(), got.Delta)
	assert.Equal(t, [][]byte{{0x01, 0x02}, {0x03, 0x04}}, got.Endorsements)
	assert.Empty(t, got.RawTx, "raw tx stays empty until broadcast")
	assert.Empty(t, got.EthTxHash)
}

func TestEnvelopeEmptyRoundTrips(t *testing.T) {
	env := &Envelope{Anchor: "just-anchor"}
	raw, err := env.Bytes()
	require.NoError(t, err)

	var got Envelope
	require.NoError(t, got.FromBytes(raw))
	assert.Equal(t, "just-anchor", got.TxID())
	assert.Nil(t, got.Delta, "an empty delta must stay omitted, not become a zero delta")
	assert.Empty(t, got.Endorsements)
}
