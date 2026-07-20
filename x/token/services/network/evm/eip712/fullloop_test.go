/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fabactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// TestFullLoopTranslateSignRecover chains the whole Week-3 Go pipeline on real fabtoken actions:
// translate (issue then spend) -> EIP-712 digest -> sign -> recover the endorser address. Together
// with the fixture endorsement verified on-chain (GoEndorsement.t.sol), this pins every link an
// endorser exercises in production: identical deltas hash to identical digests, and the contract
// accepts exactly what the signer emits.
func TestFullLoopTranslateSignRecover(t *testing.T) {
	pp := []byte("full-loop-pp")

	var issueAnchor [32]byte
	issueAnchor[31] = 0xE1
	tok := &fabactions.Output{Owner: []byte("alice"), Type: "TOK", Quantity: "0x05"}

	var spendAnchor [32]byte
	spendAnchor[31] = 0xE2
	transfer := &fabactions.TransferAction{
		Inputs: []*fabactions.TransferActionInput{{
			ID:    &token.ID{TxId: hex.EncodeToString(issueAnchor[:]), Index: 0},
			Input: tok,
		}},
		Outputs: []*fabactions.Output{{Owner: []byte("bob"), Type: "TOK", Quantity: "0x05"}},
	}

	translate := func() *statedelta.StateDelta {
		tr := statedelta.NewTranslator(spendAnchor, pp, 0)
		require.NoError(t, tr.Write(context.Background(), transfer))
		require.NoError(t, tr.AddPublicParamsDependency())
		_, err := tr.CommitTokenRequest([]byte("full-loop-request"), true)
		require.NoError(t, err)
		d, err := tr.StateDelta()
		require.NoError(t, err)

		return d
	}

	contractAddr, err := client.HexToAddress("0x1234567890123456789012345678901234567890")
	require.NoError(t, err)
	domain := Domain{ChainID: big.NewInt(31337), VerifyingContract: contractAddr}

	// two independent endorsers translate independently and sign the same digest
	digest1 := Digest(domain, translate())
	digest2 := Digest(domain, translate())
	assert.Equal(t, digest1, digest2, "independent endorsers must sign the same digest")

	signer := testSigner(t, 1)
	sig, err := signer.Sign(digest1)
	require.NoError(t, err)

	recovered, err := RecoverAddress(digest1, sig)
	require.NoError(t, err)
	assert.Equal(t, signer.Address(), recovered)
}
