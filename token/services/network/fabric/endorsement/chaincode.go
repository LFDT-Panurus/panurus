/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
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
}

func NewChaincodeEndorsementService(tmsID token2.TMSID) *ChaincodeEndorsementService {
	return &ChaincodeEndorsementService{TMSID: tmsID}
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
	env, err := ev.Endorse(context)
	if err != nil {
		return nil, err
	}

	return env, nil
}
