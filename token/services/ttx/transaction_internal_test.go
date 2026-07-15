/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This white-box (package ttx) file covers appendPayload, mergeTokenRequest, and
// mergeTransient, which are unexported.
package ttx

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRequestWithActions(actions ...*driver.TypedAction) *token.Request {
	r := token.NewRequest(nil, "anchor")
	r.Actions.Actions = actions

	return r
}

func typedAction(actionType request.ActionType, raw string) *driver.TypedAction {
	return &driver.TypedAction{Type: actionType, Raw: []byte(raw)}
}

func TestTransaction_MergeTokenRequest_FirstWrite(t *testing.T) {
	tx := &Transaction{Payload: &Payload{TokenRequest: newRequestWithActions()}}
	incoming := newRequestWithActions(typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A"))

	err := tx.mergeTokenRequest(incoming)

	require.NoError(t, err)
	assert.Same(t, incoming, tx.TokenRequest)
}

func TestTransaction_MergeTokenRequest_SupersetExtension(t *testing.T) {
	actionA := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A")
	actionB := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "B")
	tx := &Transaction{Payload: &Payload{TokenRequest: newRequestWithActions(actionA)}}
	incoming := newRequestWithActions(actionA, actionB)

	err := tx.mergeTokenRequest(incoming)

	require.NoError(t, err)
	assert.Same(t, incoming, tx.TokenRequest)
	assert.Len(t, tx.TokenRequest.Actions.Actions, 2)
}

func TestTransaction_MergeTokenRequest_ViolationShorter(t *testing.T) {
	actionA := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A")
	actionB := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "B")
	existing := newRequestWithActions(actionA, actionB)
	tx := &Transaction{Payload: &Payload{TokenRequest: existing}}
	incoming := newRequestWithActions(actionA)

	err := tx.mergeTokenRequest(incoming)

	require.Error(t, err)
	assert.Same(t, existing, tx.TokenRequest, "existing request must not be replaced on violation")
}

func TestTransaction_MergeTokenRequest_ViolationDivergentPrefix(t *testing.T) {
	actionA := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A")
	actionX := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "X")
	actionB := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "B")
	existing := newRequestWithActions(actionA)
	tx := &Transaction{Payload: &Payload{TokenRequest: existing}}
	incoming := newRequestWithActions(actionX, actionB)

	err := tx.mergeTokenRequest(incoming)

	require.Error(t, err)
	assert.Same(t, existing, tx.TokenRequest, "existing request must not be replaced on violation")
}

func TestTransaction_MergeTransient_NewKeys(t *testing.T) {
	tx := &Transaction{Payload: &Payload{Transient: network.TransientMap{"k": []byte("v1")}}}
	incoming := network.TransientMap{"k2": []byte("v3")}

	err := tx.mergeTransient(incoming)

	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), tx.Transient["k"])
	assert.Equal(t, []byte("v3"), tx.Transient["k2"])
}

func TestTransaction_MergeTransient_IdenticalValueNoConflict(t *testing.T) {
	tx := &Transaction{Payload: &Payload{Transient: network.TransientMap{"k": []byte("v1")}}}
	incoming := network.TransientMap{"k": []byte("v1")}

	err := tx.mergeTransient(incoming)

	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), tx.Transient["k"])
}

func TestTransaction_MergeTransient_ConflictingValueErrors(t *testing.T) {
	tx := &Transaction{Payload: &Payload{Transient: network.TransientMap{"k": []byte("v1")}}}
	incoming := network.TransientMap{"k": []byte("v2")}

	err := tx.mergeTransient(incoming)

	require.Error(t, err)
	assert.Equal(t, []byte("v1"), tx.Transient["k"], "existing value must not be overwritten on conflict")
}

func TestTransaction_MergeTransient_NilTarget(t *testing.T) {
	tx := &Transaction{Payload: &Payload{Transient: nil}}
	incoming := network.TransientMap{"k": []byte("v")}

	err := tx.mergeTransient(incoming)

	require.NoError(t, err)
	require.NotNil(t, tx.Transient)
	assert.Equal(t, []byte("v"), tx.Transient["k"])
}

func TestTransaction_AppendPayload_MergesBoth(t *testing.T) {
	actionA := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A")
	actionB := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "B")
	tx := &Transaction{Payload: &Payload{
		TokenRequest: newRequestWithActions(actionA),
		Transient:    network.TransientMap{"k": []byte("v1")},
	}}
	payload := &Payload{
		TokenRequest: newRequestWithActions(actionA, actionB),
		Transient:    network.TransientMap{"k": []byte("v1"), "k2": []byte("v3")},
	}

	err := tx.appendPayload(payload)

	require.NoError(t, err)
	assert.Len(t, tx.TokenRequest.Actions.Actions, 2)
	assert.Equal(t, []byte("v1"), tx.Transient["k"])
	assert.Equal(t, []byte("v3"), tx.Transient["k2"])
}

func TestTransaction_AppendPayload_TransientConflictErrors(t *testing.T) {
	actionA := typedAction(request.ActionType_ACTION_TYPE_TRANSFER, "A")
	tx := &Transaction{Payload: &Payload{
		TokenRequest: newRequestWithActions(actionA),
		Transient:    network.TransientMap{"k": []byte("v1")},
	}}
	payload := &Payload{
		TokenRequest: newRequestWithActions(actionA),
		Transient:    network.TransientMap{"k": []byte("v2")},
	}

	err := tx.appendPayload(payload)

	require.Error(t, err)
}
