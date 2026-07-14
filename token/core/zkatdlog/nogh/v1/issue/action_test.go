/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"crypto/rand"
	"io"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerialization(t *testing.T) {
	action := randomAction(math.Curves[math.BN254], rand.Reader, t)
	raw, err := action.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action2 := &Action{}
	err = action2.Deserialize(raw)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action, action2, "deserialized action is not equal to the original one")

	raw2, err := action2.Serialize()
	require.NoError(t, err, "failed to serialize a new transfer action")

	action3 := &Action{}
	err = action3.Deserialize(raw2)
	require.NoError(t, err, "failed to deserialize a new transfer action")
	assert.Equal(t, action2, action3, "deserialized action is not equal to the original one")
}

func TestDeserializeError(t *testing.T) {
	action := &Action{}
	err := action.Deserialize([]byte("invalid"))
	require.ErrorIs(t, err, ErrDeserializeIssueActionFailed)

	// Invalid version
	raw, err := proto.Marshal(&actions.IssueAction{Version: ProtocolV1 + 1})
	require.NoError(t, err)
	err = action.Deserialize(raw)
	require.ErrorIs(t, err, ErrInvalidProtocolVersion)
}

func BenchmarkActionMarshalling(b *testing.B) {
	curve := math.Curves[math.BN254]

	b.Run("With Protos", func(b *testing.B) {
		rand, err := curve.Rand()
		require.NoError(b, err, "failed to get random number")
		for range b.N {
			b.StopTimer()
			action := randomAction(curve, rand, b)
			b.StartTimer()
			_, err = action.Serialize()
			require.NoError(b, err, "failed to serialize a new transfer action")
		}
	})

	b.Run("With json", func(b *testing.B) {
		rand, err := curve.Rand()
		require.NoError(b, err, "failed to get random number")
		for range b.N {
			b.StopTimer()
			action := randomAction(curve, rand, b)
			b.StartTimer()
			_, err = json.Marshal(action)
			require.NoError(b, err, "failed to serialize a new transfer action")
		}
	})
}

func getRandomBytes(b require.TestingT, len int) []byte {
	key := make([]byte, len)
	_, err := rand.Read(key)
	require.NoError(b, err, "error getting random bytes")

	return key
}

func randomAction(curve *math.Curve, rand io.Reader, b require.TestingT) *Action {
	// generate an action at random
	issuer := getRandomBytes(b, 32)
	commitments := []*math.G1{
		curve.GenG1.Mul(curve.NewRandomZr(rand)),
		curve.GenG1.Mul(curve.NewRandomZr(rand)),
	}
	owners := [][]byte{
		getRandomBytes(b, 32),
		getRandomBytes(b, 32),
	}
	proof := getRandomBytes(b, 32)
	action, err := NewAction(issuer, commitments, owners, proof)
	require.NoError(b, err, "failed to create a new transfer action")
	action.Inputs = []*ActionInput{
		{ID: token2.ID{
			TxId:  "txid1",
			Index: 0,
		}, Token: curve.GenG1.Mul(curve.NewRandomZr(rand)).Bytes()},
		{ID: token2.ID{
			TxId:  "txid2",
			Index: 1,
		}, Token: curve.GenG1.Mul(curve.NewRandomZr(rand)).Bytes()},
	}
	action.Metadata = map[string][]byte{
		"key1": getRandomBytes(b, 32),
		"key2": getRandomBytes(b, 32),
	}

	return action
}

func TestFields(t *testing.T) {
	curve := math.Curves[math.BN254]
	action := randomAction(curve, rand.Reader, t)

	assert.Equal(t, 2, action.NumInputs())
	assert.Len(t, action.GetInputs(), 2)
	assert.Equal(t, "txid1", action.GetInputs()[0].TxId)
	assert.Equal(t, uint64(0), action.GetInputs()[0].Index)
	assert.Equal(t, "txid2", action.GetInputs()[1].TxId)
	assert.Equal(t, uint64(1), action.GetInputs()[1].Index)

	serializedInputs, err := action.GetSerializedInputs()
	require.NoError(t, err)
	assert.Len(t, serializedInputs, 2)
	assert.Equal(t, action.Inputs[0].Token, serializedInputs[0])
	assert.Equal(t, action.Inputs[1].Token, serializedInputs[1])

	assert.Nil(t, action.GetSerialNumbers())
	assert.Equal(t, action.Metadata, action.GetMetadata())
	assert.False(t, action.IsAnonymous())
	assert.Equal(t, 2, action.NumOutputs())
	assert.Len(t, action.GetOutputs(), 2)

	serializedOutputs, err := action.GetSerializedOutputs()
	require.NoError(t, err)
	assert.Len(t, serializedOutputs, 2)

	assert.Equal(t, []byte(action.Issuer), action.GetIssuer())
	assert.False(t, action.IsGraphHiding())
	require.NoError(t, action.Validate())
	assert.Nil(t, action.ExtraSigners())

	commitments, err := action.GetCommitments()
	require.NoError(t, err)
	assert.Len(t, commitments, 2)
	assert.True(t, action.Outputs[0].Data.Equals(commitments[0]))
	assert.True(t, action.Outputs[1].Data.Equals(commitments[1]))

	assert.Equal(t, rp.RangeProofType, action.ProofType)
	assert.Equal(t, action.Proof, action.GetProof())

	// Test nil inputs in GetInputs and GetSerializedInputs
	action.Inputs[0] = nil
	assert.Nil(t, action.GetInputs()[0])
	serializedInputs, err = action.GetSerializedInputs()
	require.NoError(t, err)
	assert.Nil(t, serializedInputs[0])

	// Test nil output in GetSerializedOutputs and GetCommitments
	oldOutputs := action.Outputs
	action.Outputs = []*token.Token{nil}
	_, err = action.GetSerializedOutputs()
	require.ErrorIs(t, err, ErrNilOutput)
	_, err = action.GetCommitments()
	require.ErrorIs(t, err, ErrNilOutput)
	action.Outputs = oldOutputs
}

func TestValidate(t *testing.T) {
	curve := math.Curves[math.BN254]
	action := randomAction(curve, rand.Reader, t)

	// Valid action
	require.NoError(t, action.Validate())

	// Issuer not set
	oldIssuer := action.Issuer
	action.Issuer = nil
	err := action.Validate()
	require.ErrorIs(t, err, ErrIssuerNotSet)
	action.Issuer = oldIssuer

	// Nil input
	oldInput := action.Inputs[0]
	action.Inputs[0] = nil
	err = action.Validate()
	require.ErrorIs(t, err, ErrNilInput)
	action.Inputs[0] = oldInput

	// Nil input token
	oldToken := action.Inputs[0].Token
	action.Inputs[0].Token = nil
	err = action.Validate()
	require.ErrorIs(t, err, ErrNilInputToken)
	action.Inputs[0].Token = oldToken

	// Nil input id
	oldTxId := action.Inputs[0].ID.TxId
	action.Inputs[0].ID.TxId = ""
	err = action.Validate()
	require.ErrorIs(t, err, ErrNilInputID)
	action.Inputs[0].ID.TxId = oldTxId

	// No outputs
	oldOutputs := action.Outputs
	action.Outputs = nil
	err = action.Validate()
	require.ErrorIs(t, err, ErrNoOutputs)
	action.Outputs = oldOutputs

	// Nil output
	action.Outputs = []*token.Token{nil}
	err = action.Validate()
	require.ErrorIs(t, err, ErrNilOutput)
	action.Outputs = oldOutputs

	// Empty proof
	oldProof := action.Proof
	action.Proof = []byte{}
	err = action.Validate()
	require.ErrorIs(t, err, ErrEmptyProof)
	action.Proof = oldProof

	// Valid action again
	require.NoError(t, action.Validate())
}
