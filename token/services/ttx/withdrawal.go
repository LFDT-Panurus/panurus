/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// WithdrawalRequest is the first message of the withdrawal protocol. The
// requester sends it to the issuer to declare which identity should receive
// the issued tokens. It carries no nonce or signature; those are exchanged in
// the subsequent challenge/response round-trip initiated by the issuer.
type WithdrawalRequest struct {
	TMSID         token.TMSID
	RecipientData RecipientData
	TokenType     token2.Type
	Amount        uint64
	NotAnonymous  bool
}

// WithdrawalChallenge is sent by the issuer after receiving a WithdrawalRequest.
// The issuer samples a fresh nonce so that it — not the requester — controls
// freshness, preventing replay attacks.
type WithdrawalChallenge struct {
	Nonce []byte
}

// WithdrawalResponse is sent by the requester in reply to a WithdrawalChallenge.
// Signature is a key-ownership attestation over the challenge nonce and the
// fields of the original WithdrawalRequest (built via buildAttestationMessage).
type WithdrawalResponse struct {
	Signature []byte
}

// RequestWithdrawalView is the initiator view to request an issuer the issuance of tokens.
// The view prepares an instance of WithdrawalRequest and send it to the issuer.
type RequestWithdrawalView struct {
	Issuer         view.Identity
	TokenType      token2.Type
	Amount         uint64
	TMSID          token.TMSID
	ExternalWallet bool
	Wallet         string
	NotAnonymous   bool
	RecipientData  *RecipientData
	Signers        map[string]ExternalWalletSigner
}

func NewRequestWithdrawalView(issuer view.Identity, tokenType token2.Type, amount uint64, notAnonymous bool, wallet string, tmsID token.TMSID, recipientData *RecipientData, signers map[string]ExternalWalletSigner) *RequestWithdrawalView {
	return &RequestWithdrawalView{
		Issuer:        issuer,
		TokenType:     tokenType,
		Amount:        amount,
		NotAnonymous:  notAnonymous,
		Wallet:        wallet,
		TMSID:         tmsID,
		RecipientData: recipientData,
		Signers:       signers,
	}
}

// RequestWithdrawal runs RequestWithdrawalView with the passed arguments.
// The view will generate a recipient identity and pass it to the issuer.
func RequestWithdrawal(
	ctx view.Context,
	issuer view.Identity,
	wallet string,
	tokenType token2.Type,
	amount uint64,
	notAnonymous bool,
	opts ...token.ServiceOption,
) (view.Identity, view.Session, error) {
	return RequestWithdrawalForRecipient(ctx, issuer, wallet, tokenType, amount, notAnonymous, nil, opts...)
}

// RequestWithdrawalForRecipient runs RequestWithdrawalView with the passed arguments.
// The view will send the passed recipient data to the issuer.
func RequestWithdrawalForRecipient(
	ctx view.Context,
	issuer view.Identity,
	wallet string,
	tokenType token2.Type,
	amount uint64,
	notAnonymous bool,
	recipientData *RecipientData,
	opts ...token.ServiceOption,
) (view.Identity, view.Session, error) {
	options, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to compile options")
	}
	endorsementOpts, err := CompileCollectEndorsementsOpts(opts...)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to compile collect endorsement options")
	}

	resultBoxed, err := ctx.RunView(
		NewRequestWithdrawalView(
			issuer,
			tokenType,
			amount,
			notAnonymous,
			wallet,
			options.TMSID(),
			recipientData,
			endorsementOpts.ExternalWalletSigners,
		))
	if err != nil {
		return nil, nil, err
	}

	result := resultBoxed.([]any)
	ir := result[0].(*WithdrawalRequest)

	return ir.RecipientData.Identity, result[1].(view.Session), nil
}

func (r *RequestWithdrawalView) Call(context view.Context) (any, error) {
	logger.DebugfContext(context.Context(), "Request withdrawal using wallet [%s]", r.Wallet)

	tmsID, recipientData, w, err := r.getRecipientIdentity(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get recipient data")
	}

	logger.DebugfContext(context.Context(), "Start session")
	s, err := jsession.NewTypedSessionForCaller(context, context.Initiator(), r.Issuer)
	if err != nil {
		logger.Errorf("failed to get session to [%s]: [%s]", r.Issuer, err)

		return nil, errors.Wrapf(err, "failed to get session to [%s]", r.Issuer)
	}

	wr := &WithdrawalRequest{
		TMSID:         *tmsID,
		RecipientData: *recipientData,
		TokenType:     r.TokenType,
		Amount:        r.Amount,
		NotAnonymous:  r.NotAnonymous,
	}

	logger.DebugfContext(context.Context(), "Send withdrawal request")
	if err = s.SendTyped(context.Context(), wr, TypeWithdrawalRequest); err != nil {
		logger.Errorf("failed to send withdrawal request: [%s]", err)

		return nil, errors.Wrapf(err, "failed to send withdrawal request")
	}

	// Receive the issuer-sampled challenge nonce.
	logger.DebugfContext(context.Context(), "Receive withdrawal challenge")
	challenge := &WithdrawalChallenge{}
	if err = s.ReceiveTypedWithTimeout(TypeWithdrawalChallenge, challenge, 1*time.Minute); err != nil {
		return nil, errors.Wrapf(err, "failed to receive withdrawal challenge")
	}
	if len(challenge.Nonce) == 0 {
		return nil, errors.New("withdrawal challenge missing nonce")
	}

	// Sign the issuer-controlled nonce to prove key ownership.
	message, err := buildAttestationMessage(*tmsID, nil, recipientData.Identity, false, "", challenge.Nonce, s.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build attestation message")
	}
	var sig []byte
	if r.ExternalWallet {
		signer, ok := r.Signers[r.Wallet]
		if !ok {
			return nil, errors.Errorf("no signer for wallet [%s]", r.Wallet)
		}
		sig, err = signer.Sign(recipientData.Identity, message)
	} else {
		sig, err = signRecipientAttestation(context.Context(), w, message, recipientData.Identity, true)
	}
	if err != nil {
		return nil, err
	}

	logger.DebugfContext(context.Context(), "Send withdrawal response")
	if err = s.SendTyped(context.Context(), &WithdrawalResponse{Signature: sig}, TypeWithdrawalResponse); err != nil {
		return nil, errors.Wrapf(err, "failed to send withdrawal response")
	}

	return []any{wr, s.Session()}, nil
}

func (r *RequestWithdrawalView) getRecipientIdentity(context view.Context) (*token.TMSID, *RecipientData, *token.OwnerWallet, error) {
	if r.RecipientData != nil {
		tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
		if err != nil {
			return nil, nil, nil, errors.Wrapf(err, "tms not found for [%s]", r.TMSID)
		}

		// TODO: check that RecipientData is registered

		r.ExternalWallet = true

		return new(tms.ID()), r.RecipientData, nil, nil
	}

	w := GetWallet(
		context,
		r.Wallet,
		token.WithTMSID(r.TMSID),
	)
	if w == nil {
		logger.Errorf("failed to get wallet [%s]", r.Wallet)

		return nil, nil, nil, errors.Errorf("wallet [%s:%s] not found", r.Wallet, r.TMSID)
	}

	if r.RecipientData != nil {
		return new(w.TMS().ID()), r.RecipientData, w, nil
	}

	recipientData, err := w.GetRecipientData(context.Context())
	if err != nil {
		logger.Errorf("failed to get recipient data: [%s]", err)

		return nil, nil, nil, errors.Wrapf(err, "failed to get recipient data")
	}

	return new(w.TMS().ID()), recipientData, w, nil
}

// ReceiveWithdrawalRequestView this is the view used by the issuer to receive a withdrawal request
type ReceiveWithdrawalRequestView struct{}

func NewReceiveIssuanceRequestView() *ReceiveWithdrawalRequestView {
	return &ReceiveWithdrawalRequestView{}
}

func ReceiveWithdrawalRequest(context view.Context) (*WithdrawalRequest, error) {
	requestBoxed, err := context.RunView(NewReceiveIssuanceRequestView())
	if err != nil {
		return nil, err
	}
	ir := requestBoxed.(*WithdrawalRequest)

	return ir, nil
}

func (r *ReceiveWithdrawalRequestView) Call(context view.Context) (any, error) {
	s := jsession.NewTypedSessionFromContext(context)
	request := &WithdrawalRequest{}
	if err := s.ReceiveTypedWithTimeout(TypeWithdrawalRequest, request, 1*time.Minute); err != nil {
		return nil, errors.Wrapf(err, "failed to receive withdrawal request")
	}

	logger.DebugfContext(context.Context(), "Received withdrawal request")
	tms, err := token.GetManagementService(context, token.WithTMSID(request.TMSID))
	if err != nil {
		return nil, errors.Wrapf(err, "tms not found for [%s]", request.TMSID)
	}

	// Sample a fresh nonce and send it as the challenge. The requester must
	// sign this nonce, proving it — not any replayed message — is live.
	nonce, err := GetRandomNonce()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate withdrawal challenge nonce")
	}
	logger.DebugfContext(context.Context(), "Send withdrawal challenge")
	if err = s.SendTyped(context.Context(), &WithdrawalChallenge{Nonce: nonce}, TypeWithdrawalChallenge); err != nil {
		return nil, errors.Wrapf(err, "failed to send withdrawal challenge")
	}

	// Receive the requester's signed response.
	logger.DebugfContext(context.Context(), "Receive withdrawal response")
	resp := &WithdrawalResponse{}
	if err = s.ReceiveTypedWithTimeout(TypeWithdrawalResponse, resp, 1*time.Minute); err != nil {
		return nil, errors.Wrapf(err, "failed to receive withdrawal response")
	}

	// Verify the key-ownership attestation using the issuer-controlled nonce.
	message, err := buildAttestationMessage(request.TMSID, nil, request.RecipientData.Identity, false, "", nonce, s.Info().ID, context.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build attestation message")
	}
	if err = verifyRecipientAttestation(context.Context(), tms, message, &request.RecipientData, resp.Signature, false); err != nil {
		return nil, err
	}

	if err = tms.WalletManager().RegisterRecipientIdentity(context.Context(), &request.RecipientData); err != nil {
		logger.Errorf("failed to register recipient identity: [%s]", err)

		return nil, errors.Wrapf(err, "failed to register recipient identity")
	}

	// Update the Endpoint Resolver
	caller := context.Session().Info().Caller
	logger.DebugfContext(context.Context(), "update endpoint resolver for [%s], bind to [%s]", request.RecipientData.Identity, caller)
	if err = endpoint.GetService(context).Bind(context.Context(), caller, request.RecipientData.Identity); err != nil {
		logger.DebugfContext(context.Context(), "failed binding [%s] to [%s]", request.RecipientData.Identity, caller)

		return nil, errors.Wrapf(err, "failed binding [%s] to [%s]", request.RecipientData.Identity, caller)
	}

	return request, nil
}
