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

// TransientPublicParamsKey is the transient map key used to carry the raw public parameters
// from the initiator to the responder.
const TransientPublicParamsKey = "public_params"

// TransientPublicParamsSigKey is the transient map key used to carry the detached issuer
// signature over the raw public parameters bytes. It is present only on re-setup, when a
// TMS already exists for the namespace and the setup responder requires proof that the
// submission is authorized by a current issuer.
const TransientPublicParamsSigKey = "public_params_sig"

// PublicParamsSignature is a detached signature over the raw public parameters bytes,
// produced by a current issuer to authorize a re-setup.
type PublicParamsSignature struct {
	// SignerIdentity is the identity of the issuer that produced Signature.
	SignerIdentity view.Identity `json:"signer_identity"`
	// Signature is the detached signature over the raw public parameters bytes.
	Signature []byte `json:"signature"`
}

// SetupPublicParamsView is the initiator of the public parameters setup/update protocol.
type SetupPublicParamsView struct {
	TMSID           token.TMSID
	TxID            driver.TxID
	PublicParamsRaw []byte
	// PublicParamsSig is the detached issuer signature over PublicParamsRaw, required on
	// re-setup and nil on first-time setup.
	PublicParamsSig *PublicParamsSignature
	// Endorsers are the identities of the FSC node that play the role of endorser
	Endorsers []view.Identity

	// EndorserService is the endorser service
	EndorserService EndorserService
}

// NewSetupPublicParamsView returns a new instance of SetupPublicParamsView
func NewSetupPublicParamsView(
	TMSID token.TMSID,
	txID driver.TxID,
	publicParamsRaw []byte,
	ppSig *PublicParamsSignature,
	endorsers []view.Identity,
	endorserService EndorserService,
) *SetupPublicParamsView {
	return &SetupPublicParamsView{
		TMSID:           TMSID,
		TxID:            txID,
		PublicParamsRaw: publicParamsRaw,
		PublicParamsSig: ppSig,
		Endorsers:       endorsers,
		EndorserService: endorserService,
	}
}

func (s *SetupPublicParamsView) Call(ctx view.Context) (any, error) {
	logger.DebugfContext(ctx.Context(), "request public params setup from tms id [%s]", s.TMSID)

	tx, err := s.EndorserService.NewTransaction(
		ctx,
		fabric.WithCreator(s.TxID.Creator),
		fabric.WithNonce(s.TxID.Nonce),
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create endorser transaction")
	}

	tx.SetProposal(s.TMSID.Namespace, ChaincodeVersion, SetupFunction)
	if err := tx.EndorseProposal(); err != nil {
		return nil, errors.WithMessagef(err, "failed to endorse proposal")
	}

	// transient fields
	if err := tx.SetTransientState(TransientTMSIDKey, s.TMSID); err != nil {
		return nil, errors.WithMessagef(err, "failed to set TMS ID transient")
	}
	if err := tx.SetTransient(TransientPublicParamsKey, s.PublicParamsRaw); err != nil {
		return nil, errors.WithMessagef(err, "failed to set public params transient")
	}
	if s.PublicParamsSig != nil {
		sigRaw, err := json.Marshal(s.PublicParamsSig)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to marshal public params signature")
		}
		if err := tx.SetTransient(TransientPublicParamsSigKey, sigRaw); err != nil {
			return nil, errors.WithMessagef(err, "failed to set public params signature transient")
		}
	}

	logger.DebugfContext(ctx.Context(), "request endorsement on tx [%s] to [%v]...", tx.ID(), s.Endorsers)
	err = s.EndorserService.CollectEndorsements(ctx, tx, 2*time.Minute, s.Endorsers...)
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
