/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	dauditor "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	session2 "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	view3 "github.com/LFDT-Panurus/panurus/token/services/utils/view"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Auditor wraps the functionalities of the auditor service and the audit db for the ttx service
type Auditor struct {
	dauditor.Service
	dauditor.StoreService
}

// NewAuditorFromTMSID returns a new Auditor for the given TMS ID
func NewAuditorFromTMSID(sp token.ServiceProvider, tmsID token.TMSID) (*Auditor, error) {
	auditServiceProvider, err := dauditor.GetServiceProvider(sp)
	if err != nil {
		return nil, errors.Join(ErrProvider, err)
	}
	auditService, auditStoreService, err := auditServiceProvider.AuditorService(tmsID)
	if err != nil {
		return nil, errors.Join(ErrProvider, err)
	}

	return &Auditor{
		Service:      auditService,
		StoreService: auditStoreService,
	}, nil
}

// NewAuditor returns a new Auditor for the given wallet.
// This constructor uses NewAuditorFromTMSID with the wallet's TMSID.
func NewAuditor(sp token.ServiceProvider, w *token.AuditorWallet) (*Auditor, error) {
	return NewAuditorFromTMSID(sp, w.TMS().ID())
}

// Validate checks if the token request in the transaction is valid according to audit rules.
// It delegates to the underlying audit service for validation.
func (a *Auditor) Validate(tx *Transaction) error {
	return a.Service.Validate(tx.Context, tx.TokenRequest)
}

// Audit extracts the list of inputs and outputs from the passed transaction.
// In addition, Audit acquires locks on the enrollment IDs involved in the transaction.
// The caller MUST call Release() to unlock these enrollment IDs after processing.
//
// IMPORTANT: The defer Release() statement MUST be placed immediately after checking
// the error returned by Audit(). This ensures locks are released even if subsequent
// operations fail. Example:
//
//	inputs, outputs, err := auditor.Audit(ctx, tx)
//	if err != nil {
//	    return errors.Wrap(err, "audit failed")
//	}
//	defer auditor.Release(ctx, tx)
//
// Note: The semaphore-based locking mechanism handles context cancellation during
// lock acquisition, ensuring proper cleanup in case of timeouts or cancellations.
func (a *Auditor) Audit(ctx context.Context, tx *Transaction) (*token.InputStream, *token.OutputStream, error) {
	return a.Service.Audit(ctx, tx)
}

// Release unlocks the passed enrollment IDs.
func (a *Auditor) Release(ctx context.Context, tx *Transaction) {
	a.Service.Release(ctx, tx)
}

// Transactions returns an iterator of transaction records filtered by the given params.
func (a *Auditor) Transactions(ctx context.Context, params db.QueryTransactionsParams, pagination db.Pagination) (*db.PageTransactionsIterator, error) {
	return a.StoreService.Transactions(ctx, params, pagination)
}

// NewPaymentsFilter returns a programmable filter over the payments sent or received by enrollment IDs.
func (a *Auditor) NewPaymentsFilter() *auditdb.PaymentsFilter {
	return a.StoreService.NewPaymentsFilter()
}

// NewHoldingsFilter returns a programmable filter over the holdings owned by enrollment IDs.
func (a *Auditor) NewHoldingsFilter() *auditdb.HoldingsFilter {
	return a.StoreService.NewHoldingsFilter()
}

// SetStatus sets the status of the audit records with the passed transaction id to the passed status
func (a *Auditor) SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error {
	return a.StoreService.SetStatus(ctx, txID, status, message)
}

// GetTokenRequest retrieves the serialized token request for the given transaction ID.
// Returns an error if the transaction is not found in the audit database.
func (a *Auditor) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	return a.Service.GetTokenRequest(ctx, txID)
}

// Check performs a health check on the auditor service and returns any issues found.
// It delegates to the underlying audit service for the check.
func (a *Auditor) Check(ctx context.Context) ([]string, error) {
	return a.Service.Check(ctx)
}

// RegisterAuditorView is a view that registers an auditor responder view for handling
// audit requests from token transaction initiators.
type RegisterAuditorView struct {
	TMSID     token.TMSID
	AuditView view.View
}

// NewRegisterAuditorView creates a new RegisterAuditorView with the provided audit view
// and service options. Returns nil if the options cannot be compiled.
func NewRegisterAuditorView(auditView view.View, opts ...token.ServiceOption) *RegisterAuditorView {
	options, err := token.CompileServiceOptions(opts...)
	if err != nil {
		return nil
	}

	return &RegisterAuditorView{
		AuditView: auditView,
		TMSID:     options.TMSID(),
	}
}

// Call registers the audit view as a responder for AuditingViewInitiator requests.
// This allows the auditor to respond to audit requests from transaction initiators.
func (r *RegisterAuditorView) Call(context view.Context) (any, error) {
	// register responder
	if err := view2.GetRegistry(context).RegisterResponder(r.AuditView, &AuditingViewInitiator{}); err != nil {
		return nil, errors.Wrapf(err, "failed to register auditor view")
	}

	return nil, nil
}

// AuditingViewInitiator is a view that initiates an auditing session with an auditor.
// It sends the transaction to the auditor and receives back a signature.
// The local flag indicates whether the auditor is the same as the initiator (self-audit).
type AuditingViewInitiator struct {
	tx                               *Transaction
	local                            bool
	skipAuditorSignatureVerification bool
}

// newAuditingViewInitiator creates a new AuditingViewInitiator for the given transaction.
// The local parameter indicates if this is a self-audit scenario.
// The skipAuditorSignatureVerification parameter allows bypassing signature verification for testing.
func newAuditingViewInitiator(tx *Transaction, local, skipAuditorSignatureVerification bool) *AuditingViewInitiator {
	return &AuditingViewInitiator{tx: tx, local: local, skipAuditorSignatureVerification: skipAuditorSignatureVerification}
}

// Call initiates an auditing session (local or remote), sends the transaction to the auditor,
// receives the auditor's signature, verifies it, and adds it to the transaction.
// Returns the session used for communication.
func (a *AuditingViewInitiator) Call(context view.Context) (any, error) {
	var err error
	var session view.Session

	if a.local {
		session, err = a.startLocal(context)
	} else {
		session, err = a.startRemote(context)
	}
	if err != nil {
		return nil, errors.WithMessagef(err, "failed starting auditing session")
	}

	// Receive signature
	logger.DebugfContext(context.Context(), "Receiving signature for [%s]", a.tx.ID())

	var signaturePayload SignaturePayload
	if err := session2.NewTypedSession(context, session).ReceiveTypedWithTimeout(TypeSignature, &signaturePayload, time.Minute); err != nil {
		logger.ErrorfContext(context.Context(), "failed to read audit event: %s", err)

		return nil, errors.WithMessagef(err, "failed to read audit event")
	}
	signature := signaturePayload.Signature
	logger.DebugfContext(context.Context(), "reply received from %s", a.tx.Opts.Auditor)

	auditorIdentity, err := a.verifyAuditorSignature(context, signature)
	if err != nil {
		return nil, errors.Wrapf(err, "failed verifying auditor signature")
	}

	a.tx.TokenRequest.AddAuditorSignature(auditorIdentity, signature)

	logger.Debugf("auditor signature verified")

	return session, nil
}

// startRemote establishes a remote session with the auditor and sends the transaction.
// This is used when the auditor is a different node than the transaction initiator.
func (a *AuditingViewInitiator) startRemote(context view.Context) (view.Session, error) {
	logger.DebugfContext(context.Context(), "Starting remote auditing session with [%s] for [%s]", a.tx.Opts.Auditor.UniqueID(), a.tx.ID())
	session, err := context.GetSession(a, a.tx.Opts.Auditor)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting session")
	}

	// Send transaction
	txRaw, err := a.tx.Bytes()
	if err != nil {
		return nil, err
	}
	err = session2.NewTypedSession(context, session).SendTyped(context.Context(), &TransactionPayload{Raw: txRaw}, TypeTransaction)
	if err != nil {
		return nil, errors.Wrap(err, "failed sending transaction")
	}

	return session, nil
}

// startLocal creates a local bidirectional channel for self-auditing scenarios.
// This is used when the auditor is the same node as the transaction initiator (e.g., an issuer
// that is also an auditor). Since FSC doesn't support opening sessions to self, this creates
// a fake bidirectional channel to simulate the communication between initiator and responder.
// The responder view is executed in a separate goroutine using the right side of the channel,
// while the initiator uses the left side.
func (a *AuditingViewInitiator) startLocal(context view.Context) (view.Session, error) {
	logger.DebugfContext(context.Context(), "Starting local auditing for %s", a.tx.ID())

	// This code is executed everytime the auditor is the same as the
	// initiator of a token transaction.
	// For example, if an issuer is also an auditor, then when the issuer asks
	// for auditing, the issuer is essentially talking to itself.
	// FSC does not yet support opening communication session to itself,
	// therefore we create a fake bidirectional communication channel between
	// the AuditingViewInitiator view and its registered responder.
	// Notice also the use of view.AsResponder(right) to run the responder
	// using a predefined session.
	// This code can be removed once FSC supports opening communication session to self.

	// Prepare a bidirectional channel
	// Give to the responder view the right channel, and keep for
	// AuditingViewInitiator the left channel.
	biChannel, err := NewLocalBidirectionalChannel(context.Context(), "", context.ID(), "", nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating session")
	}
	left := biChannel.LeftSession()
	right := biChannel.RightSession()

	// Send transaction
	txRaw, err := a.tx.Bytes()
	if err != nil {
		return nil, err
	}
	err = session2.NewTypedSession(context, left).SendTyped(context.Context(), &TransactionPayload{Raw: txRaw}, TypeTransaction)
	if err != nil {
		return nil, errors.Wrap(err, "failed sending transaction")
	}

	// execute the auditor responder using the fake communication session
	responderView, err := view2.GetRegistry(context).GetResponder(&AuditingViewInitiator{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get auditor view")
	}
	// Run the view in a new goroutine
	view3.RunView(logger, context, responderView, view.AsResponder(right))

	return left, nil
}

// verifyAuditorSignature verifies that the received signature is valid and was created by
// one of the authorized auditors listed in the public parameters. It marshals the transaction
// for auditing, then iterates through all registered auditors to find one whose signature
// verifies correctly. Returns the identity of the auditor who signed, or an error if no
// valid signature is found. Signature verification can be skipped for testing purposes.
func (a *AuditingViewInitiator) verifyAuditorSignature(context view.Context, signature []byte) (token.Identity, error) {
	logger.DebugfContext(context.Context(), "Validate auditing")

	if a.skipAuditorSignatureVerification {
		return a.tx.Opts.Auditor, nil
	}

	// check the signature
	signed, err := a.tx.MarshallToAudit()
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling message to sign")
	}
	logger.DebugfContext(context.Context(), "Verifying auditor signature on [%s][%s][%s]", a.tx.Opts.Auditor, utils.Hashable(signed), a.tx.ID())

	for _, auditorID := range a.tx.TokenService().PublicParametersManager().PublicParameters().Auditors() {
		v, err := a.tx.TokenService().SigService().AuditorVerifier(context.Context(), auditorID)
		if err != nil {
			logger.DebugfContext(context.Context(), "failed to get auditor verifier for [%s]", auditorID)

			continue
		}
		logger.DebugfContext(context.Context(), "Verify auditor signature")
		if err := v.Verify(signed, signature); err != nil {
			logger.ErrorfContext(context.Context(), "failed verifying auditor signature [%s][%s][%s]", auditorID, utils.Hashable(signed), a.tx.TokenRequest.Anchor)
		} else {
			logger.DebugfContext(context.Context(), "auditor signature verified [%s][%s][%s]", auditorID, base64.StdEncoding.EncodeToString(signature), utils.Hashable(signed))

			return auditorID, nil
		}
	}

	return nil, errors.Errorf("failed verifying auditor signature [%s][%s]", utils.Hashable(signed).String(), a.tx.TokenRequest.Anchor)
}

// AuditApproveView is a responder view that handles audit approval requests.
// It audits the transaction, appends audit records to the database, signs the transaction,
// and sends the signature back to the initiator.
type AuditApproveView struct {
	w  *token.AuditorWallet
	tx *Transaction
}

// NewAuditApproveView creates a new AuditApproveView for the given auditor wallet and transaction.
func NewAuditApproveView(w *token.AuditorWallet, tx *Transaction) *AuditApproveView {
	return &AuditApproveView{w: w, tx: tx}
}

// Call processes the audit approval by:
// 1. Appending audit records to the database (with lock acquisition/release)
// 2. Signing the transaction with the auditor's key
// 3. Sending the signature back to the initiator
// 4. Caching the token request for faster future lookups
// 5. Recording metrics for the approval process
func (a *AuditApproveView) Call(context view.Context) (any, error) {
	start := time.Now()
	// Append audit records
	if err := auditor.Get(context, a.w).Append(context.Context(), a.tx); err != nil {
		return nil, errors.Wrapf(err, "failed appending audit records for transaction %s", a.tx.ID())
	}

	if err := a.signAndSendBack(context); err != nil {
		return nil, err
	}

	// cache the token request into the tokens db
	t, err := tokens.GetService(context, a.tx.TMSID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get tokens db for [%s]", a.tx.TMSID())
	}
	if err := t.CacheRequest(context.Context(), a.tx.TokenRequest); err != nil {
		logger.Warnf("failed to cache token request [%s], this might cause delay, investigate when possible: [%s]", a.tx.TokenRequest.Anchor, err)
	}

	labels := []string{
		"network", a.tx.Network(),
		"channel", a.tx.Channel(),
		"namespace", a.tx.Namespace(),
	}
	metrics := GetMetrics(context)
	metrics.AuditApprovedTransactions.With(labels...).Add(1)
	metrics.AuditApprovalDuration.With(labels...).Observe(time.Since(start).Seconds())

	return nil, nil
}

// signAndSendBack retrieves the auditor's identity and signing key, marshals the transaction
// for auditing, signs it, and sends the signature back to the initiator through the session.
func (a *AuditApproveView) signAndSendBack(context view.Context) error {
	logger.DebugfContext(context.Context(), "Signing and sending back transaction... [%s]", a.tx.ID())
	// Sign
	aid, err := a.w.GetAuditorIdentity()
	if err != nil {
		return errors.WithMessagef(err, "failed getting auditor identity for node [%s]", context.Me())
	}
	signer, err := a.w.GetSigner(context.Context(), aid)
	if err != nil {
		return errors.WithMessagef(err, "failed getting signing identity for auditor identity [%s]", aid)
	}

	raw, err := a.tx.MarshallToAudit()
	if err != nil {
		return errors.Wrapf(err, "failed marshalling tx [%s] to audit", a.tx.ID())
	}

	logger.DebugfContext(context.Context(), "Audit Approve [%s][%s][%s]", aid, utils.Hashable(raw), a.tx.TokenRequest.Anchor)
	sigma, err := signer.Sign(raw)
	if err != nil {
		return errors.Wrapf(err, "failed sign audit message for tx [%s]", a.tx.ID())
	}

	logger.DebugfContext(context.Context(), "auditor sending sigma back", utils.Hashable(sigma))
	if err := session2.NewTypedSessionFromContext(context).SendTyped(context.Context(), &SignaturePayload{Signature: sigma}, TypeSignature); err != nil {
		return errors.WithMessagef(err, "failed sending back auditor signature")
	}
	logger.DebugfContext(context.Context(), "Signing and sending back transaction...done [%s]", a.tx.ID())

	return nil
}
