/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownedLedgerOutput is a ledger output owned by the identity in owner.
type ownedLedgerOutput struct{ owner []byte }

func (o *ownedLedgerOutput) Serialize() ([]byte, error) { return o.owner, nil }
func (o *ownedLedgerOutput) IsRedeem() bool             { return false }
func (o *ownedLedgerOutput) GetOwner() []byte           { return o.owner }

// newOwnedOutputBlankedTx returns a transaction with one transfer output owned by a local
// owner wallet, whose metadata is absent.
func newOwnedOutputBlankedTx(t *testing.T) *Transaction {
	t.Helper()
	transferAction := &drivermock.TransferAction{}
	transferAction.NumOutputsReturns(1)
	transferAction.GetOutputsReturns([]driver.Output{&ownedLedgerOutput{owner: []byte("bob-pseudonym")}})
	ts := &drivermock.TransferService{}
	ts.DeserializeTransferActionReturns(transferAction, nil)
	auth := &drivermock.Authorization{}
	auth.IsMineStub = func(_ context.Context, tok *token2.Token) (string, []string, bool) {
		return "bob-wallet", nil, string(tok.Owner) == "bob-pseudonym"
	}
	dtms := &drivermock.TokenManagerService{}
	dtms.TransferServiceReturns(ts)
	dtms.AuthorizationReturns(auth)
	ppm := &drivermock.PublicParamsManager{}
	ppm.PublicParametersReturns(&drivermock.PublicParameters{})
	dtms.PublicParamsManagerReturns(ppm)
	vault := &drivermock.Vault{}
	vault.QueryEngineReturns(&drivermock.QueryEngine{})
	vp := &tokenmock.VaultProvider{}
	vp.VaultReturns(vault, nil)
	tmsID := token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}
	ms, err := token.NewManagementService(tmsID, dtms, logging.MustGetLogger(), vp, nil, nil)
	require.NoError(t, err)

	req := token.NewRequest(ms, "a_tx")
	req.Actions.Actions = []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")}}
	// A blanked output decodes from the wire as an empty entry.
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{TransferMetadata: &driver.TransferMetadata{
		Outputs: []*driver.TransferOutputMetadata{{}},
	}}}

	return &Transaction{Payload: &Payload{ID: "a_tx", tmsID: tmsID, TokenRequest: req}}
}

// TestAcceptAndEndorse_RefuseMissingOwnedOutputMetadata verifies that AcceptView and EndorseView
// refuse a transaction that lacks the metadata of an output owned by this node.
// They fail before they store records or send an ack: both need a service from the context (issue #2713).
func TestAcceptAndEndorse_RefuseMissingOwnedOutputMetadata(t *testing.T) {
	views := map[string]func(tx *Transaction) view.View{
		"accept":  func(tx *Transaction) view.View { return NewAcceptView(tx) },
		"endorse": func(tx *Transaction) view.View { return NewEndorseView(tx) },
	}
	for name, newView := range views {
		t.Run(name, func(t *testing.T) {
			serviceRequests := 0
			ctx := &fakeViewContext{t: t, getService: func(any) (any, error) {
				serviceRequests++

				return nil, errors.New("no service in test")
			}}

			_, err := newView(newOwnedOutputBlankedTx(t)).Call(ctx)
			require.ErrorContains(t, err, "missing metadata for output [0,0] owned by wallet [bob-wallet]")
			assert.Zero(t, serviceRequests)
		})
	}
}
