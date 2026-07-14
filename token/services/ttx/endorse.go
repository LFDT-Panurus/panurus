/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"bytes"
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// EndorseView is used to accept tokens with, possibly, the need to generate some signature for the tokens that belong to this node.
// This is a view executed by a responder.
// This view is to be used in conjunction with CollectEndorsementsView.
// Usually, EndorseView is preceded by an invocation of `tx.ReceiveTransaction(context)` or alike.
type EndorseView struct {
	tx *Transaction
}

// NewEndorseView returns an instance of the EndorseView embedding the passed transaction.
// The view expects that the transaction has been already checked by the business logic for containing the expected context.
func NewEndorseView(tx *Transaction) *EndorseView {
	return &EndorseView{tx: tx}
}

// Call handles the signature requests with the respect to the transaction this view has been constructed with.
// It expects to deal with messages coming from CollectEndorsementsView.
// Here are the steps:
// - Storage of the transaction's records.
// - Generation of the required signatures.
// - Reception of the endorsed transaction
// - Acknowledgement of the reception of the endorsed transaction
// - Finalization
func (s *EndorseView) Call(context view.Context) (any, error) {
	// validate input
	if s.tx == nil {
		return nil, errors.Wrapf(ErrInvalidInput, "transaction is nil")
	}

	// store transaction in the token transaction database
	if err := StoreTransactionRecords(context, s.tx); err != nil {
		return nil, errors.Wrapf(err, "failed storing transaction records %s", s.tx.ID())
	}

	// handle signature requests
	if err := s.handleSignatureRequests(context); err != nil {
		return nil, errors.Join(err, ErrHandlingSignatureRequests)
	}

	// receive endorsed transaction
	receivedTx, err := s.receiveTransaction(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed receiving transaction")
	}

	// acknowledge reception.
	if err := s.ack(context, receivedTx); err != nil {
		return nil, errors.Wrapf(err, "failed acknowledging transaction")
	}

	// cache the token request into the tokens db, should we use the received token request?
	sp, err := GetStorageProvider(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to storage provider")
	}
	if err := sp.CacheRequest(context.Context(), s.tx.TMSID(), s.tx.TokenRequest); err != nil {
		logger.Warnf("failed to cache token request [%s], this might cause delay, investigate when possible: [%s]", s.tx.TokenRequest.Anchor, err)
	}

	return s.tx, nil
}

// handleSignatureRequests processes the signature requests for the transaction this view has been constructed with.
// It expects to deal with messages coming from CollectEndorsementsView.
func (s *EndorseView) handleSignatureRequests(context view.Context) error {
	// Process signature requests
	logger.DebugfContext(context.Context(), "check expected number of requests to sign for tx id [%s]", s.tx.ID())
	requiredSigners, err := extractRequiredSigners(context.Context(), s.tx.TMS.SigService(), s.tx.Request())
	if err != nil {
		return errors.Wrapf(err, "failed collecting requests of signature")
	}

	logger.DebugfContext(context.Context(), "expect [%d] requests to sign for tx id [%s]", len(requiredSigners), s.tx.ID())

	typedSession := jsession.NewTypedSessionFromContext(context)

	tokenRequestToSign, err := s.tx.TokenRequest.MarshalToSign()
	if err != nil {
		return errors.Wrap(err, "failed to marshal token request to sign")
	}

	for i, signerIdentity := range requiredSigners {
		signatureRequest := &SignatureRequest{}

		if i == 0 && s.tx.FromSignatureRequest != nil {
			signatureRequest = s.tx.FromSignatureRequest
		} else {
			logger.DebugfContext(context.Context(), "receiving signature request...")
			if err := typedSession.ReceiveTypedWithTimeout(TypeSignatureRequest, signatureRequest, time.Minute); err != nil {
				return errors.Wrap(err, "failed reading signature request")
			}
		}

		// check for the expected identity
		if !signatureRequest.Signer.Equal(signerIdentity) {
			return errors.Wrapf(
				ErrSignerIdentityMismatch,
				"signature request's signer does not match the expected signer, [%s] != [%s], required signatures [%d]",
				signatureRequest.Signer,
				signerIdentity,
				len(requiredSigners),
			)
		}

		// sign the token request with the expected identity
		sigService := s.tx.TokenService().SigService()
		signer, err := sigService.GetSigner(context.Context(), signerIdentity)
		if err != nil {
			return errors.Wrapf(err, "cannot find signer for [%s]", signerIdentity)
		}
		sigma, err := signer.Sign(tokenRequestToSign)
		if err != nil {
			return errors.Wrapf(err, "failed signing request")
		}
		logger.DebugfContext(context.Context(), "Send back signature [%s][%s]", signerIdentity, utils.Hashable(sigma))
		err = typedSession.SendTyped(context.Context(), &SignaturePayload{Signature: sigma}, TypeSignature)
		if err != nil {
			return errors.Wrapf(err, "failed sending signature back")
		}
	}

	return nil
}

// receiveTransaction is used to intercept the last round of transaction distribution from CollectEndorsementsView.
// Indeed, after having collected the auditor signatures, if needed, and the approval,
// CollectEndorsementsView distributes the token request with the additional signatures.
func (s *EndorseView) receiveTransaction(context view.Context) ([]byte, error) {
	logger.DebugfContext(context.Context(), "receive transaction...")
	tx, err := ReceiveTransaction(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed receiving transaction")
	}

	// check that the content of the token request match
	m1, err := s.tx.TokenRequest.MarshalToSign()
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token request to sign from the local transaction")
	}
	m2, err := tx.TokenRequest.MarshalToSign()
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token request to sign from the remote transaction")
	}
	if !bytes.Equal(m1, m2) {
		return nil, errors.Errorf("token request's signer does not match the expected signer")
	}

	return tx.FromRaw, nil
}

// ack sends back an acknowledgement message to the initiator of the endorsement collection process.
func (s *EndorseView) ack(context view.Context, msg []byte) error {
	inSession := context.Session()
	// Send back an acknowledgement
	idProvider, err := dep.GetNetworkIdentityProvider(context)
	if err != nil {
		return errors.Wrapf(err, "failed getting identity provider")
	}
	defaultIdentity := idProvider.DefaultIdentity()
	logger.DebugfContext(context.Context(), "signing ack response [%s] with identity [%s]", utils.Hashable(msg), defaultIdentity)
	signer, err := idProvider.GetSigner(defaultIdentity)
	if err != nil {
		return errors.WithMessagef(err, "failed to get signer for default identity")
	}
	sigma, err := signer.Sign(msg)
	if err != nil {
		return errors.WithMessagef(err, "failed to sign ack response")
	}
	logger.DebugfContext(context.Context(), "ack response: [%s] from [%s]", utils.Hashable(sigma), defaultIdentity)
	if err := jsession.NewTypedSession(context, inSession).SendTyped(context.Context(), &SignaturePayload{Signature: sigma}, TypeSignature); err != nil {
		return errors.WithMessagef(err, "failed sending ack")
	}

	return nil
}

// extractRequiredSigners extracts from the given token request a list of identities that can generate a signature over it.
func extractRequiredSigners(ctx context.Context, sigService dep.SignatureService, request *token.Request) ([]token.Identity, error) {
	issuerSigners := request.IssueSigners()
	transferSigners := request.TransferSigners()
	res := make([]token.Identity, 0, len(issuerSigners)+len(transferSigners))
	for _, signer := range issuerSigners {
		if appendUnwrapped(&res, signer) {
			continue
		}
		res = append(res, signer)
	}
	for _, signer := range transferSigners {
		if appendUnwrapped(&res, signer) {
			continue
		}
		res = append(res, signer)
	}
	subset := make([]token.Identity, 0, len(res))
	for _, res := range res {
		if sigService.IsMe(ctx, res) {
			subset = append(subset, res)
		}
	}

	return subset, nil
}

// appendUnwrapped appends the component identities of a composite identity (multisig or policy)
// to res and returns true; returns false when the identity is not a composite type.
func appendUnwrapped(res *[]token.Identity, signer token.Identity) bool {
	if multiSigners, _, _ := multisig.Unwrap(signer); len(multiSigners) != 0 {
		*res = append(*res, multiSigners...)

		return true
	}
	if pi, ok, _ := boolpolicy.Unwrap(signer); ok && pi != nil {
		for _, b := range pi.Identities {
			*res = append(*res, token.Identity(b))
		}

		return true
	}

	return false
}
