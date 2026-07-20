/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

var (
	testPP      = []byte("test-public-params")
	testRequest = []byte("test-token-request")
)

func testAnchor(v byte) [32]byte {
	var a [32]byte
	for i := range a {
		a[i] = v
	}

	return a
}

// finish drives the tail of the responder sequence and returns the finalized delta.
func finish(t *testing.T, tr *Translator) *StateDelta {
	t.Helper()
	require.NoError(t, tr.AddPublicParamsDependency())
	_, err := tr.CommitTokenRequest(testRequest, true)
	require.NoError(t, err)
	d, err := tr.StateDelta()
	require.NoError(t, err)

	return d
}

// fakeSetup is a minimal SetupAction (the SDK ships no counterfeiter fake for it).
type fakeSetup struct {
	params []byte
	err    error
}

func (f *fakeSetup) GetSetupParameters() ([]byte, error) { return f.params, f.err }

func issueAction(outputs [][]byte, metadata map[string][]byte) *mock.IssueAction {
	a := &mock.IssueAction{}
	a.GetSerializedOutputsReturns(outputs, nil)
	a.GetMetadataReturns(metadata)
	a.IsGraphHidingReturns(false)
	a.GetInputsReturns(nil)
	a.GetSerializedInputsReturns(nil, nil)
	a.GetSerialNumbersReturns(nil)

	return a
}

func transferAction(inputs []*token.ID, serializedInputs [][]byte, outputs [][]byte, redeemAt map[int]bool, metadata map[string][]byte) *mock.TransferAction {
	a := &mock.TransferAction{}
	a.NumOutputsReturns(len(outputs))
	a.IsRedeemAtCalls(func(i int) bool { return redeemAt[i] })
	a.SerializeOutputAtCalls(func(i int) ([]byte, error) { return outputs[i], nil })
	a.GetMetadataReturns(metadata)
	a.IsGraphHidingReturns(false)
	a.GetInputsReturns(inputs)
	a.GetSerializedInputsReturns(serializedInputs, nil)
	a.GetSerialNumbersReturns(nil)

	return a
}

func TestIssueMapping(t *testing.T) {
	anchor := testAnchor(0x11)
	tr := NewTranslator(anchor, testPP, 3)
	require.NoError(t, tr.Write(context.Background(), issueAction([][]byte{[]byte("out-0"), []byte("out-1")}, map[string][]byte{"mk": []byte("mv")})))
	d := finish(t, tr)

	require.Len(t, d.Outputs, 2)
	assert.Equal(t, keys.ComputeTokenID(anchor, 0), d.Outputs[0].TokenID)
	assert.Equal(t, keys.OutputSNMarker(anchor, 0, []byte("out-0")), d.Outputs[0].SNMarker)
	assert.Equal(t, keys.ComputeTokenID(anchor, 1), d.Outputs[1].TokenID)
	assert.Equal(t, []byte("out-1"), d.Outputs[1].TokenData)
	assert.Empty(t, d.SpentRefs)

	require.Len(t, d.MetadataKeys, 1)
	assert.Equal(t, keys.IssueMetadataKey("mk"), d.MetadataKeys[0])
	assert.Equal(t, []byte("mv"), d.MetadataVals[0])

	assert.EqualValues(t, 3, d.PublicParamsVersion)
	assert.Equal(t, crypto.SHA256(testPP), d.PublicParamsHash[:])
	assert.Equal(t, crypto.SHA256(testRequest), d.TokenRequestHash[:])
	assert.False(t, d.IsSetup)
}

// TestTransferMapping covers the content-bound spend refs and the redeem slot semantics: a redeem
// output is skipped but its index is consumed, exactly as the Fabric translator enumerates outputs.
func TestTransferMapping(t *testing.T) {
	inAnchor := testAnchor(0x22)
	inTxID := hex.EncodeToString(inAnchor[:])
	inputs := []*token.ID{{TxId: inTxID, Index: 5}}
	serialized := [][]byte{[]byte("input-token-bytes")}

	anchor := testAnchor(0x33)
	tr := NewTranslator(anchor, testPP, 0)
	require.NoError(t, tr.Write(context.Background(), transferAction(
		inputs, serialized,
		[][]byte{[]byte("keep-0"), []byte("redeemed"), []byte("keep-2")},
		map[int]bool{1: true},
		map[string][]byte{"tk": []byte("tv")},
	)))
	d := finish(t, tr)

	// the spend ref is the content-bound marker of the input, recomputed from its id AND bytes
	require.Len(t, d.SpentRefs, 1)
	assert.Equal(t, keys.OutputSNMarker(inAnchor, 5, []byte("input-token-bytes")), d.SpentRefs[0])

	// slot 1 is redeemed: skipped, but slot 2 keeps output index 2
	require.Len(t, d.Outputs, 2)
	assert.Equal(t, keys.ComputeTokenID(anchor, 0), d.Outputs[0].TokenID)
	assert.Equal(t, keys.ComputeTokenID(anchor, 2), d.Outputs[1].TokenID)

	require.Len(t, d.MetadataKeys, 1)
	assert.Equal(t, keys.TransferMetadataKey("tk"), d.MetadataKeys[0])
}

// TestCounterAcrossActions pins the cross-action counter: issue advances by len(outputs), transfer
// by NumOutputs() including redeem slots (verified against the Fabric translator, plan Week 3).
func TestCounterAcrossActions(t *testing.T) {
	anchor := testAnchor(0x44)
	tr := NewTranslator(anchor, testPP, 0)
	require.NoError(t, tr.Write(context.Background(), issueAction([][]byte{[]byte("i0"), []byte("i1")}, nil)))
	require.NoError(t, tr.Write(context.Background(), transferAction(
		nil, nil,
		[][]byte{[]byte("redeemed"), []byte("t1")},
		map[int]bool{0: true},
		nil,
	)))
	require.NoError(t, tr.Write(context.Background(), issueAction([][]byte{[]byte("i2")}, nil)))
	d := finish(t, tr)

	// indexes: issue 0,1; transfer consumes 2 (redeem, skipped) and 3; second issue takes 4
	require.Len(t, d.Outputs, 4)
	assert.Equal(t, keys.ComputeTokenID(anchor, 0), d.Outputs[0].TokenID)
	assert.Equal(t, keys.ComputeTokenID(anchor, 1), d.Outputs[1].TokenID)
	assert.Equal(t, keys.ComputeTokenID(anchor, 3), d.Outputs[2].TokenID)
	assert.Equal(t, keys.ComputeTokenID(anchor, 4), d.Outputs[3].TokenID)
}

func TestGraphHidingSpendsBySerial(t *testing.T) {
	a := &mock.TransferAction{}
	a.NumOutputsReturns(0)
	a.IsGraphHidingReturns(true)
	a.GetSerialNumbersReturns([]string{"sn-1", "sn-2"})
	a.GetInputsReturns(nil)
	a.GetSerializedInputsReturns(nil, nil)
	a.GetMetadataReturns(nil)

	tr := NewTranslator(testAnchor(0x55), testPP, 0)
	require.NoError(t, tr.Write(context.Background(), a))
	d := finish(t, tr)

	require.Len(t, d.SpentRefs, 2)
	assert.Equal(t, keys.SpentRefForSerial([]byte("sn-1")), d.SpentRefs[0])
	assert.Equal(t, keys.SpentRefForSerial([]byte("sn-2")), d.SpentRefs[1])
	assert.Empty(t, d.Outputs)
}

func TestSetupMapping(t *testing.T) {
	tr := NewTranslator(testAnchor(0x66), testPP, 4)
	require.NoError(t, tr.Write(context.Background(), &fakeSetup{params: []byte("pp-v5")}))
	d := finish(t, tr)

	assert.True(t, d.IsSetup)
	assert.Equal(t, []byte("pp-v5"), d.SetupParameters)
	assert.Empty(t, d.Outputs)
	assert.Empty(t, d.SpentRefs)
	assert.EqualValues(t, 4, d.PublicParamsVersion, "a setup delta asserts the CURRENT params it replaces")
}

func TestErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown action type", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		require.Error(t, tr.Write(ctx, struct{}{}))
	})

	t.Run("setup mixed with transfer", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		require.NoError(t, tr.Write(ctx, &fakeSetup{params: []byte("pp")}))
		require.Error(t, tr.Write(ctx, transferAction(nil, nil, nil, nil, nil)))
	})

	t.Run("transfer then setup", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		require.NoError(t, tr.Write(ctx, issueAction([][]byte{[]byte("o")}, nil)))
		require.Error(t, tr.Write(ctx, &fakeSetup{params: []byte("pp")}))
	})

	t.Run("duplicate setup", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		require.NoError(t, tr.Write(ctx, &fakeSetup{params: []byte("pp")}))
		require.Error(t, tr.Write(ctx, &fakeSetup{params: []byte("pp2")}))
	})

	t.Run("inputs and serialized inputs length mismatch", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		a := transferAction([]*token.ID{{TxId: hex.EncodeToString(make([]byte, 32)), Index: 0}}, nil, nil, nil, nil)
		require.Error(t, tr.Write(ctx, a))
	})

	t.Run("non-anchor input txid", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		a := transferAction([]*token.ID{{TxId: "not-hex", Index: 0}}, [][]byte{[]byte("b")}, nil, nil, nil)
		require.Error(t, tr.Write(ctx, a))
	})

	t.Run("finalize without pp dependency", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		_, err := tr.CommitTokenRequest(testRequest, true)
		require.NoError(t, err)
		_, err = tr.StateDelta()
		require.Error(t, err)
	})

	t.Run("finalize without token request hash", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		require.NoError(t, tr.AddPublicParamsDependency())
		_, err := tr.StateDelta()
		require.Error(t, err)
	})

	t.Run("empty token request", func(t *testing.T) {
		tr := NewTranslator(testAnchor(1), testPP, 0)
		_, err := tr.CommitTokenRequest(nil, true)
		require.Error(t, err)
	})
}
