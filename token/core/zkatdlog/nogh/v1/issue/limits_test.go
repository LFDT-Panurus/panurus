/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"strconv"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

func baseValidAction() *Action {
	curve := math.Curves[math.BN254]

	return &Action{
		Issuer: []byte("issuer"),
		Inputs: []*ActionInput{
			{ID: token2.ID{TxId: "txid1", Index: 0}, Token: []byte("token1")},
		},
		Outputs: []*token.Token{
			{Owner: []byte("owner1"), Data: curve.GenG1},
		},
		ProofType: rp.RangeProofType,
		Proof:     []byte("proof"),
		Metadata:  map[string][]byte{},
	}
}

func TestAction_Validate_TooManyInputs(t *testing.T) {
	mk := func(n int) *Action {
		a := baseValidAction()
		a.Inputs = make([]*ActionInput, n)
		for i := range a.Inputs {
			a.Inputs[i] = &ActionInput{ID: token2.ID{TxId: "txid", Index: uint64(i)}, Token: []byte("token")}
		}

		return a
	}
	require.NoError(t, mk(MaxInputs-1).Validate())
	require.NoError(t, mk(MaxInputs).Validate())
	require.ErrorIs(t, mk(MaxInputs+1).Validate(), ErrTooManyInputs)
}

func TestAction_Validate_TooManyOutputs(t *testing.T) {
	curve := math.Curves[math.BN254]
	mk := func(n int) *Action {
		a := baseValidAction()
		a.Outputs = make([]*token.Token, n)
		for i := range a.Outputs {
			a.Outputs[i] = &token.Token{Owner: []byte("owner"), Data: curve.GenG1}
		}

		return a
	}
	require.NoError(t, mk(MaxOutputs-1).Validate())
	require.NoError(t, mk(MaxOutputs).Validate())
	require.ErrorIs(t, mk(MaxOutputs+1).Validate(), ErrTooManyOutputs)
}

func TestAction_Validate_ProofTooLarge(t *testing.T) {
	mk := func(n int) *Action {
		a := baseValidAction()
		a.Proof = make([]byte, n)

		return a
	}
	require.NoError(t, mk(MaxProofBytes-1).Validate())
	require.NoError(t, mk(MaxProofBytes).Validate())
	require.ErrorIs(t, mk(MaxProofBytes+1).Validate(), ErrProofTooLarge)
}

func TestAction_Validate_MetadataLimits(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		mk := func(n int) *Action {
			a := baseValidAction()
			a.Metadata = make(map[string][]byte, n)
			for i := range n {
				a.Metadata["k"+strconv.Itoa(i)] = []byte("v")
			}

			return a
		}
		require.NoError(t, mk(MaxMetadataEntries-1).Validate())
		require.NoError(t, mk(MaxMetadataEntries).Validate())
		require.ErrorIs(t, mk(MaxMetadataEntries+1).Validate(), ErrTooManyMetadataEntries)
	})
	t.Run("key bytes", func(t *testing.T) {
		mk := func(n int) *Action {
			a := baseValidAction()
			a.Metadata = map[string][]byte{string(make([]byte, n)): []byte("v")}

			return a
		}
		require.NoError(t, mk(MaxMetadataKeyBytes-1).Validate())
		require.NoError(t, mk(MaxMetadataKeyBytes).Validate())
		require.ErrorIs(t, mk(MaxMetadataKeyBytes+1).Validate(), ErrMetadataKeyTooLarge)
	})
	t.Run("value bytes", func(t *testing.T) {
		mk := func(n int) *Action {
			a := baseValidAction()
			a.Metadata = map[string][]byte{"k": make([]byte, n)}

			return a
		}
		require.NoError(t, mk(MaxMetadataValueBytes-1).Validate())
		require.NoError(t, mk(MaxMetadataValueBytes).Validate())
		require.ErrorIs(t, mk(MaxMetadataValueBytes+1).Validate(), ErrMetadataValueTooLarge)
	})
}

func marshalIssueAction(t *testing.T, inputs, outputs, proofLen int) []byte {
	t.Helper()
	ia := &actions.IssueAction{
		Version: ProtocolV1,
		Issuer:  &protosv1.Identity{Raw: []byte("issuer")},
	}
	for range inputs {
		ia.Inputs = append(ia.Inputs, &actions.IssueActionInput{Token: []byte("t")})
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &actions.IssueActionOutput{Token: &actions.Token{Owner: []byte("o")}})
	}
	if proofLen > 0 {
		ia.Proof = &actions.Proof{ProofType: &actions.Proof_Proof{Proof: make([]byte, proofLen)}}
	}
	raw, err := proto.Marshal(ia)
	require.NoError(t, err)

	return raw
}

func TestAction_Deserialize_TooManyInputs(t *testing.T) {
	a := &Action{}
	require.NoError(t, a.Deserialize(marshalIssueAction(t, MaxInputs, 1, 1)))
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, MaxInputs+1, 1, 1)), ErrTooManyInputs)
}

func TestAction_Deserialize_TooManyOutputs(t *testing.T) {
	a := &Action{}
	require.NoError(t, a.Deserialize(marshalIssueAction(t, 1, MaxOutputs, 1)))
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, 1, MaxOutputs+1, 1)), ErrTooManyOutputs)
}

func TestAction_Deserialize_ProofTooLarge(t *testing.T) {
	a := &Action{}
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, 1, 1, MaxProofBytes+1)), ErrProofTooLarge)
}

func TestAction_Deserialize_ManyTinyInputs(t *testing.T) {
	// Many minimal-size inputs should be rejected purely on count, not size.
	a := &Action{}
	require.ErrorIs(t, a.Deserialize(marshalIssueAction(t, MaxInputs+1, 1, 1)), ErrTooManyInputs)
}

// TestAction_Deserialize_RejectsBeforeCryptographicWork proves that an oversized proof is
// rejected by the resource-limit check before any cryptographic verifier work would run:
// Deserialize returns near-instantly and never reaches proof-specific unmarshalling.
func TestAction_Deserialize_RejectsBeforeCryptographicWork(t *testing.T) {
	raw := marshalIssueAction(t, 1, 1, MaxProofBytes+1)

	start := time.Now()
	a := &Action{}
	err := a.Deserialize(raw)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, ErrProofTooLarge)
	require.Less(t, elapsed, 50*time.Millisecond, "oversized proof must be rejected near-instantly, before any cryptographic work")
}
