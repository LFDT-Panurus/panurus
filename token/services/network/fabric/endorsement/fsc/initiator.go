/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// TransientApprovalMetadataKey is the transient map key used to carry optional application-level
// approval metadata from the initiator to the responder.
const TransientApprovalMetadataKey = "approval_metadata"

// RequestApprovalView is the initiator of the request approval protocol
type RequestApprovalView struct {
	TMSID      token.TMSID
	TxID       driver.TxID
	RequestRaw []byte
	// Nonce, if not nil it will be appended to the messages to sign.
	// This is to be used only for testing.
	Nonce []byte
	// Endorsers are the identities of the FSC node that play the role of endorser
	Endorsers []view.Identity
	// Metadata carries optional application-level key-value pairs forwarded to the approver via transient data.
	Metadata driver.TransientMap

	// EndorserService is the endorser service
	EndorserService EndorserService
	// TokenManagementSystemProvider
	TokenManagementSystemProvider TokenManagementSystemProvider
}

// NewRequestApprovalView returns a new instance of RequestApprovalView
func NewRequestApprovalView(
	TMSID token.TMSID,
	txID driver.TxID,
	requestRaw []byte,
	nonce []byte,
	endorsers []view.Identity,
	endorserService EndorserService,
	metadata driver.TransientMap,
) *RequestApprovalView {
	return &RequestApprovalView{
		TMSID:           TMSID,
		TxID:            txID,
		RequestRaw:      requestRaw,
		Nonce:           nonce,
		Endorsers:       endorsers,
		EndorserService: endorserService,
		Metadata:        metadata,
	}
}

func (r *RequestApprovalView) Call(ctx view.Context) (any, error) {
	logger.DebugfContext(ctx.Context(), "request approval from tms id [%s]", r.TMSID)

	tx, err := r.EndorserService.NewTransaction(
		ctx,
		fabric.WithCreator(r.TxID.Creator),
		fabric.WithNonce(r.TxID.Nonce),
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create endorser transaction")
	}

	tx.SetProposal(r.TMSID.Namespace, ChaincodeVersion, InvokeFunction)
	if err := tx.EndorseProposal(); err != nil {
		return nil, errors.WithMessagef(err, "failed to endorse proposal")
	}

	// transient fields
	if err := tx.SetTransientState(TransientTMSIDKey, r.TMSID); err != nil {
		return nil, errors.WithMessagef(err, "failed to set TMS ID transient")
	}
	if err := tx.SetTransient(TransientTokenRequestKey, r.RequestRaw); err != nil {
		return nil, errors.WithMessagef(err, "failed to set token request transient")
	}
	if len(r.Metadata) > 0 {
		metadataRaw, err := json.Marshal(r.Metadata)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to marshal approval metadata")
		}
		if err := tx.SetTransient(TransientApprovalMetadataKey, metadataRaw); err != nil {
			return nil, errors.WithMessagef(err, "failed to set approval metadata transient")
		}
	}

	logger.DebugfContext(ctx.Context(), "request endorsement on tx [%s] to [%v]...", tx.ID(), r.Endorsers)
	err = r.EndorserService.CollectEndorsements(ctx, tx, 2*time.Minute, r.Endorsers...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to collect endorsements")
	}
	logger.DebugfContext(ctx.Context(), "request endorsement done")

	// Return envelope
	env, err := tx.Envelope()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to retrieve envelope for endorsement")
	}
	logger.DebugfContext(ctx.Context(), "envelope ready")

	return env, nil
}
