/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/chaincode"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	// InvokeFunction is the name of the function to use to request the approval of a token request
	InvokeFunction = "invoke"
)

type ChaincodeEndorsementService struct {
	TMSID token2.TMSID
	// EndorserSelection constrains which peers may endorse the invocations this
	// service issues. The zero value leaves Fabric's default discovery in charge.
	EndorserSelection config.EndorserSelection
}

// NewChaincodeEndorsementService returns a chaincode endorsement service for the given
// TMS. selection constrains the peers Fabric discovery may return as endorsers; pass the
// zero value to keep the default discovery behaviour.
func NewChaincodeEndorsementService(tmsID token2.TMSID, selection config.EndorserSelection) *ChaincodeEndorsementService {
	return &ChaincodeEndorsementService{TMSID: tmsID, EndorserSelection: selection}
}

func (e *ChaincodeEndorsementService) Endorse(context view.Context, requestRaw []byte, signer view.Identity, txID driver.TxID, metadata driver.TransientMap) (driver.Envelope, error) {
	ev := chaincode.NewEndorseView(
		e.TMSID.Namespace,
		InvokeFunction,
	).WithNetwork(
		e.TMSID.Network,
	).WithChannel(
		e.TMSID.Channel,
	).WithSignerIdentity(
		signer,
	).WithTransientEntry(
		"token_request", requestRaw,
	).WithTxID(
		fabric.TxID{
			Nonce:   txID.Nonce,
			Creator: txID.Creator,
		},
	)
	for k, v := range metadata {
		ev = ev.WithTransientEntry(k, v)
	}
	ev = withEndorserSelection(e, ev)
	env, err := ev.Endorse(context)
	if err != nil {
		return nil, e.endorsementError(err)
	}

	return env, nil
}

// withEndorserSelection applies this service's endorser selection to an invocation
// builder.
//
// It is a free function rather than a method because it is generic over the builder type:
// the FSC view types are unexported, so the only way to name one is to infer it from the
// argument. Keeping it here also makes the step the endorse path takes testable on its
// own, which Endorse is not — Endorse needs a live Fabric network.
func withEndorserSelection[T config.EndorserSelectable[T]](e *ChaincodeEndorsementService, v T) T {
	return config.ApplyEndorserSelection(v, e.EndorserSelection)
}

// endorsementError annotates a failed endorsement with the endorser restriction that was
// in force, and leaves an unrestricted endorsement's error untouched.
//
// A restriction no peer satisfies — an MSP ID that does not exist, or an organization
// whose peers cannot satisfy the chaincode's endorsement policy — otherwise surfaces as a
// discovery failure that names neither an endorser nor the configuration key responsible.
func (e *ChaincodeEndorsementService) endorsementError(err error) error {
	if !e.EndorserSelection.IsSet() {
		return err
	}

	return config.NoUsableEndorserError(err, "endorse for ["+e.TMSID.String()+"]", e.EndorserSelection.Explain())
}

// SetupPublicParams is not supported for chaincode-based endorsement: public parameters
// are set/updated exclusively via the chaincode Init lifecycle callback for this endorsement
// mode.
func (e *ChaincodeEndorsementService) SetupPublicParams(view.Context, []byte, view.Identity, driver.TxID) (driver.Envelope, error) {
	return nil, errors.Errorf("public parameters setup via endorsement is not supported for chaincode endorsement; use the chaincode Init lifecycle instead")
}
