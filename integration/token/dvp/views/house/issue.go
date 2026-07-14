/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package house

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/uniqueness"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// IssueHouse contains the input information to issue a token
type IssueHouse struct {
	// IssuerWallet is the issuer's wallet to use
	IssuerWallet string
	// Recipient is an identifier of the recipient identity
	Recipient string
	// Address is the address of the house to issue
	Address string
	// Valuation is the valuation of the house to issue
	Valuation uint64
}

type IssueHouseView struct {
	*IssueHouse
}

func (p *IssueHouseView) Call(context view.Context) (any, error) {
	// As a first step operation, the issuer contacts the recipient's FSC node
	// to ask for the identity to use to assign ownership of the freshly created token.
	// Notice that, this step would not be required if the issuer knew already which
	// identity the recipient wants to use.
	recipient, err := nfttx.RequestRecipientIdentity(context, view.Identity(p.Recipient))
	assert.NoError(err, "failed getting recipient identity")

	// At this point, the issuer is ready to prepare the token transaction.
	// The issuer creates an anonymous transaction (this means that the result Fabric transaction will be signed using idemix),
	// and specify the auditor that must be contacted to approve the operation
	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting identity provider")
	tx, err := nfttx.NewAnonymousTransaction(
		context,
		nfttx.WithAuditor(idProvider.Identity("auditor")),
	)
	assert.NoError(err, "failed creating issue transaction")

	// The issuer adds a new issue operation to the transaction following the instruction received
	wallet := nfttx.GetIssuerWallet(context, p.IssuerWallet)
	assert.NotNil(wallet, "issuer wallet [%s] not found", p.IssuerWallet)
	h := &House{
		Address:   p.Address,
		Valuation: p.Valuation,
	}
	// The issuer enforce uniqueness of the token by computing a unique identifier for the passed house.
	uniqueID, err := uniqueness.GetService(context).ComputeID(context.Context(), h.Address)
	assert.NoError(err, "failed computing unique ID")

	err = tx.Issue(wallet, h, recipient, nfttx.WithUniqueID(uniqueID))
	assert.NoError(err, "failed adding new issued token")

	// The issuer is ready to collect all the required signatures.
	// In this case, the issuer's and the auditor's signatures.
	// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative Fabric transaction.
	// This is all done in one shot running the following view.
	// Before completing, all recipients receive the approved Fabric transaction.
	// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
	// the token transaction valid.
	_, err = context.RunView(nfttx.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to sign issue transaction")

	// Last but not least, the issuer sends the transaction for ordering and waits for transaction finality.
	_, err = context.RunView(nfttx.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit issue transaction")

	return h.LinearID, nil
}

type IssueHouseViewFactory struct{}

func (p *IssueHouseViewFactory) NewView(in []byte) (view.View, error) {
	f := &IssueHouseView{IssueHouse: &IssueHouse{}}
	err := json.Unmarshal(in, f.IssueHouse)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
