/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// TypeHTLCTerms is the envelope message-type discriminator for the HTLC Terms
// exchange. It lives with the htlc service rather than the generic session package.
const TypeHTLCTerms = "htlc_terms"

// Terms contains the details of the htlc to be examined
type Terms struct {
	ReclamationDeadline time.Duration
	TMSID1              token.TMSID
	Type1               token2.Type
	Amount1             uint64
	TMSID2              token.TMSID
	Type2               token2.Type
	Amount2             uint64
}

// Bytes serializes the terms
func (t *Terms) Bytes() ([]byte, error) {
	return json.Marshal(t)
}

// FromBytes unmarshals terms
func (t *Terms) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, t)
}

// Validate checks the terms
func (t *Terms) Validate() error {
	if t.ReclamationDeadline <= 0 {
		return errors.New("reclamation deadline should be larger than zero")
	}
	if t.Type1 == "" || t.Type2 == "" {
		return errors.New("types should be set")
	}
	if t.Amount1 <= 0 || t.Amount2 <= 0 {
		return errors.New("amounts should be larger than zero")
	}

	return nil
}

// DistributeTermsView holds the terms and the recipient identity to be used by the view
type DistributeTermsView struct {
	recipient view.Identity
	terms     *Terms
}

// NewDistributeTermsView creates a view which distributes the terms to the recipient
func NewDistributeTermsView(recipient view.Identity, terms *Terms) *DistributeTermsView {
	return &DistributeTermsView{
		recipient: recipient,
		terms:     terms,
	}
}

func (v *DistributeTermsView) Call(context view.Context) (any, error) {
	sess, err := context.GetSession(context.Initiator(), v.recipient)
	if err != nil {
		return nil, err
	}
	if err := session.NewTypedSession(context, sess).SendTyped(context.Context(), v.terms, TypeHTLCTerms); err != nil {
		return nil, errors.Wrapf(err, "failed sending terms")
	}

	return nil, nil
}

type termsReceiverView struct{}

// ReceiveTerms runs the termsReceiverView and returns the received terms
func ReceiveTerms(context view.Context) (*Terms, error) {
	terms, err := context.RunView(&termsReceiverView{})
	if err != nil {
		return nil, err
	}

	return terms.(*Terms), nil
}

func (v *termsReceiverView) Call(context view.Context) (any, error) {
	terms := &Terms{}
	if err := session.NewTypedSessionFromContext(context).ReceiveTyped(TypeHTLCTerms, terms); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling terms")
	}

	return terms, nil
}
