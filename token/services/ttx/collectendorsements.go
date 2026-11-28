/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/json"
	maps0 "maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	session2 "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/sig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"go.uber.org/zap/zapcore"
)

// distributionListEntry represents a party in the transaction distribution list,
// containing their identity information and role (auditor or participant).
type distributionListEntry struct {
	IsMe     bool
	LongTerm view.Identity
	ID       view.Identity
	// EIDs lists the enrollment IDs of the remote parties that share LongTerm.
	EIDs    []string
	Auditor bool
}

//go:generate counterfeiter -o dep/mock/external_wallet_signer.go -fake-name ExternalWalletSigner . ExternalWalletSigner

// ExternalWalletSigner defines the interface for signing with external wallets
// that are not managed by the local node.
type ExternalWalletSigner interface {
	Sign(party view.Identity, message []byte) ([]byte, error)
	Done() error
}

// verifierGetterFunc is a function type for retrieving a verifier for a given identity.
type verifierGetterFunc func(ctx context.Context, identity view.Identity) (token.Verifier, error)

// SignatureRequest represents a request for a party to sign a transaction.
type SignatureRequest struct {
	TX     []byte
	Signer view.Identity
}

// Bytes returns the serialization of this struct
func (sr *SignatureRequest) Bytes() ([]byte, error) {
	return json.Marshal(sr)
}

type CollectEndorsementsView struct {
	tx   *Transaction
	Opts *EndorsementsOpts

	// sessionsMu guards sessions: on the fail-fast path, distribution workers
	// still in flight can read the cache while the deferred cleanup deletes from it.
	sessionsMu sync.Mutex
	sessions   map[string]view.Session
}

// NewCollectEndorsementsView returns an instance of the CollectEndorsementsView struct.
// This view does the following:
// 1. It collects all the required signatures
// to authorize any issue and transfer operation contained in the token transaction.
// 2. It invokes the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
// 3. Before completing, all recipients receive the approved transaction.
// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
// the token transaction valid.
func NewCollectEndorsementsView(tx *Transaction, opts ...token.ServiceOption) *CollectEndorsementsView {
	options, err := CompileCollectEndorsementsOpts(opts...)
	if err != nil {
		panic(err)
	}

	return &CollectEndorsementsView{tx: tx, Opts: options, sessions: map[string]view.Session{}}
}

// Call executes the view.
// This view does the following:
// 1. It collects all the required signatures
// to authorize any issue and transfer operation contained in the token transaction.
// 2. It invokes the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
// 3. Before completing, all recipients receive the approved transaction.
// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
// the token transaction valid.
func (c *CollectEndorsementsView) Call(context view.Context) (any, error) {
	metrics := GetMetrics(context)
	start := time.Now()

	externalWallets := make(map[string]ExternalWalletSigner)

	// Ensure Done() is called on all external wallets regardless of errors
	defer c.CleanupExternalWallets(context, externalWallets)

	// Close all endorsement sessions on every return path. Leaking the auditor
	// session on an error path keeps its audit-DB enrollment-ID lock held.
	defer c.cleanupSessions(context.Context())

	// 1. First collect signatures on the token request
	issueSigmas, err := c.requestSignaturesOnIssues(context, externalWallets)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed requesting signatures on issues")
	}

	transferSigmas, err := c.requestSignaturesOnTransfers(context, externalWallets)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed requesting signatures on transfers")
	}

	// Add the signatures to the token request
	logger.DebugfContext(context.Context(), "Add the signatures to the token request")
	if !c.tx.TokenRequest.AppendSignatures(mergeSigmas(issueSigmas, transferSigmas)) {
		return nil, errors.New("failed appending signatures to token request, some signatures are missing")
	}

	// 2. Audit
	if !c.Opts.SkipAuditing {
		_, err := c.requestAudit(context)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed requesting auditing")
		}
	}
	// 3. Endorse and return the transaction envelope
	if !c.Opts.SkipApproval {
		logger.DebugfContext(context.Context(), "Request approval from endorser")
		_, err = c.requestApproval(context)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed requesting approval")
		}
	}
	// Distribute Env to all parties
	distributionList := append(IssueDistributionList(c.tx.TokenRequest), TransferDistributionList(c.tx.TokenRequest)...)
	logger.DebugfContext(context.Context(), "distribute tx to [%d] involved parties", len(distributionList))
	if err := c.distributeTxToParties(context, distributionList, nil); err != nil {
		logger.ErrorfContext(context.Context(), "failed distributing tx: %s", err)

		return nil, errors.WithMessagef(err, "failed distributing tx")
	}

	// Sessions are closed by the deferred cleanupSessions call.
	logger.DebugfContext(context.Context(), "CollectEndorsementsView done.")

	labels := []string{
		"network", c.tx.Network(),
		"channel", c.tx.Channel(),
		"namespace", c.tx.Namespace(),
	}
	metrics.EndorsedTransactions.With(labels...).Add(1)
	metrics.EndorsementDuration.With(labels...).Observe(time.Since(start).Seconds())

	return nil, nil
}

// requestSignaturesOnIssues collects signatures from all issuers involved in the transaction's issue operations.
// It delegates to requestSignatures with the appropriate issuer verifier function.
func (c *CollectEndorsementsView) requestSignaturesOnIssues(context view.Context, externalWallets map[string]ExternalWalletSigner) (map[string][]byte, error) {
	logger.DebugfContext(context.Context(), "collecting signature on [%d] request issue", c.tx.TokenRequest.Metadata.NumIssues())

	// Use IssueSigners() - the action context is preserved in metadata and used by AppendSignatures()
	return c.requestSignatures(
		c.tx.TokenRequest.IssueSigners(),
		c.tx.TokenService().SigService().IssuerVerifier,
		context,
		externalWallets,
	)
}

// requestSignaturesOnTransfers collects signatures from all owners involved in the transaction's transfer operations.
// It delegates to requestSignatures with the appropriate owner verifier function.
func (c *CollectEndorsementsView) requestSignaturesOnTransfers(context view.Context, externalWallets map[string]ExternalWalletSigner) (map[string][]byte, error) {
	logger.DebugfContext(context.Context(), "collecting signature on [%d] request transfer", c.tx.TokenRequest.Metadata.NumTransfers())

	// Use TransferSigners() - the action context is preserved in metadata and used by AppendSignatures()
	return c.requestSignatures(
		c.tx.TokenRequest.TransferSigners(),
		c.tx.TokenService().SigService().OwnerVerifier,
		context,
		externalWallets,
	)
}

// requestSignatures collects signatures from the specified signers for the token request.
// It handles multiple signature scenarios:
// - Multi-signature identities: recursively collects signatures from all component signers
// - Policy identities: collects signatures from policy components (respecting WithPolicySigners if set)
// - Local signers: generates signatures using locally available signing keys
// - External wallet signers: delegates signing to external wallet providers
// - Remote signers: requests signatures from remote parties via network sessions (one goroutine per party)
// Returns a map of signer identity unique IDs to their signatures.
// tryMultiSigSignature handles the case where signerIdentity is a multi-sig identity: it
// recursively collects signatures from every component signer and joins them. handled is false
// (with a nil sigma and error) when signerIdentity isn't a multi-sig identity.
func (c *CollectEndorsementsView) tryMultiSigSignature(signerIdentity view.Identity, verifierGetter verifierGetterFunc, context view.Context, externalWallets map[string]ExternalWalletSigner) (sigma []byte, handled bool, err error) {
	multiSigners, ok, err := multisig.Unwrap(signerIdentity)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed unwrapping multi-sig identity [%s]", signerIdentity)
	}
	if !ok {
		return nil, false, nil
	}

	logger.DebugfContext(context.Context(), "found multi-sig identity [%s], request multi-sig signature to [%d] parties", signerIdentity, len(multiSigners))
	// collect the signatures from multiSigners
	multiSignersSigmas, err := c.requestSignatures(multiSigners, verifierGetter, context, externalWallets)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed requesting signatures")
	}
	logger.DebugfContext(context.Context(), "collected [%d] signatures for multi-sig identity [%s]", len(multiSignersSigmas), signerIdentity)
	sigma, err = multisig.JoinSignatures(multiSigners, multiSignersSigmas)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed joining multi-sig signatures")
	}

	return sigma, true, nil
}

// tryPolicySignature handles the case where signerIdentity is a policy identity: it collects
// signatures from the relevant policy components and joins them. handled is false (with a nil
// sigma and error) when signerIdentity isn't a policy identity.
func (c *CollectEndorsementsView) tryPolicySignature(signerIdentity view.Identity, verifierGetter verifierGetterFunc, context view.Context, externalWallets map[string]ExternalWalletSigner) (sigma []byte, handled bool, err error) {
	pi, ok, err := boolpolicy.Unwrap(signerIdentity)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed unwrapping policy identity [%s]", signerIdentity)
	}
	if !ok {
		return nil, false, nil
	}

	componentIDs := make([]token.Identity, len(pi.Identities))
	for idx, b := range pi.Identities {
		componentIDs[idx] = b
	}
	// collectIDs is the subset we actually request signatures from.
	// If the caller supplied WithPolicySigners, only contact those
	// components; the absent slots stay nil in the PolicySignature,
	// which satisfies OR branches without unnecessary network calls.
	collectIDs := c.policyCollectIDs(componentIDs)
	logger.DebugfContext(context.Context(), "found policy identity [%s], collecting signatures from [%d/%d] components", signerIdentity, len(collectIDs), len(componentIDs))
	componentSigmas, err := c.requestSignatures(collectIDs, verifierGetter, context, externalWallets)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed requesting policy signatures")
	}
	sigma, err = boolpolicy.JoinSignatures(componentIDs, componentSigmas)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed joining policy signatures")
	}

	return sigma, true, nil
}

// tryExternalWalletSignature handles the case where a wallet is bound to signerIdentity locally
// and has an ExternalWalletSigner registered for it. Probing the wallet first lets callers who
// have explicitly registered an ExternalWalletSigner short-circuit in O(1), avoiding the x509
// parse + BCCSP key load that GetSigner runs for identities whose private key is held outside
// the local BCCSP (e.g. in an external KMS). handled is false when no such wallet+signer exists;
// the caller should then fall through to the local-signer path, since ExternalWalletSigner
// registration is an explicit opt-in, not a strict requirement.
func (c *CollectEndorsementsView) tryExternalWalletSignature(context view.Context, signerIdentity view.Identity, requestRaw []byte, externalWallets map[string]ExternalWalletSigner) (sigma []byte, handled bool, err error) {
	w, err := c.tx.TokenService().WalletManager().OwnerWallet(context.Context(), signerIdentity)
	if err != nil {
		// no local wallet for this identity is not an error here, just "not handled by this path"
		return nil, false, nil //nolint:nilerr
	}
	ews := c.Opts.ExternalWalletSigner(w.ID())
	if ews == nil {
		return nil, false, nil
	}

	logger.DebugfContext(context.Context(), "found wallet for party [%s], request external signature", signerIdentity)
	externalWallets[w.ID()] = ews
	sigma, err = c.signExternal(context.Context(), signerIdentity, ews, requestRaw)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed signing external for party [%s]", signerIdentity)
	}

	return sigma, true, nil
}

// tryLocalSignature handles the case where a signer is locally bound to signerIdentity. handled
// is false when no local signer is found.
func (c *CollectEndorsementsView) tryLocalSignature(context view.Context, signerIdentity view.Identity, requestRaw []byte) (sigma []byte, handled bool, err error) {
	signer, err := c.tx.TokenService().SigService().GetSigner(context.Context(), signerIdentity)
	if err != nil {
		logger.DebugfContext(context.Context(), "failed to find a signer for party [%s]: [%s]", signerIdentity, err)

		return nil, false, nil
	}

	logger.DebugfContext(context.Context(), "found signer for party [%s], request local signature", signerIdentity)
	sigma, err = c.signLocal(context.Context(), signerIdentity, signer, requestRaw)
	if err != nil {
		return nil, true, errors.WithMessagef(err, "failed signing local for party [%s]", signerIdentity)
	}

	return sigma, true, nil
}

// tryNonRemoteSignature tries each local signing strategy in order (multi-sig, policy, external
// wallet, local signer) for a single signer identity. handled is false when none applied and the
// caller should fall back to a remote signature request.
func (c *CollectEndorsementsView) tryNonRemoteSignature(
	i int,
	signerIdentity view.Identity,
	verifierGetter verifierGetterFunc,
	context view.Context,
	externalWallets map[string]ExternalWalletSigner,
	requestRaw []byte,
) ([]byte, bool, error) {
	logger.DebugfContext(context.Context(), "collecting signature [%d] on request from [%s]", i, signerIdentity)

	if sigma, handled, err := c.tryMultiSigSignature(signerIdentity, verifierGetter, context, externalWallets); handled || err != nil {
		return sigma, handled, err
	}
	if sigma, handled, err := c.tryPolicySignature(signerIdentity, verifierGetter, context, externalWallets); handled || err != nil {
		return sigma, handled, err
	}
	if sigma, handled, err := c.tryExternalWalletSignature(context, signerIdentity, requestRaw, externalWallets); handled || err != nil {
		return sigma, handled, err
	}
	if sigma, handled, err := c.tryLocalSignature(context, signerIdentity, requestRaw); handled || err != nil {
		return sigma, handled, err
	}

	return nil, false, nil
}

// It handles multiple signature scenarios:
// - Multi-signature identities: recursively collects signatures from all component signers
// - Policy identities: collects signatures from policy components (respecting WithPolicySigners if set)
// - Local signers: generates signatures using locally available signing keys
// - External wallet signers: delegates signing to external wallet providers
// - Remote signers: requests signatures from remote parties via network sessions
// Returns a map of signer identity unique IDs to their signatures.
//
//nolint:gocognit // collects signatures from remote and local signers over the same request bytes; splitting risks a signer being asked to sign something other than what every other signer saw.
func (c *CollectEndorsementsView) requestSignatures(signers []view.Identity, verifierGetter verifierGetterFunc, context view.Context, externalWallets map[string]ExternalWalletSigner) (map[string][]byte, error) {
	logger.DebugfContext(context.Context(), "Request %d signatures", len(signers))
	requestRaw, err := c.tx.TokenRequest.MarshalToSign()
	if err != nil {
		return nil, err
	}
	txRaw, err := c.tx.Bytes(context.Context())
	if err != nil {
		return nil, err
	}

	sigmas := make(map[string][]byte)
	var remoteSigners []view.Identity
	remoteSeen := collections.NewSet[string]()
	for i, signerIdentity := range signers {
		sigma, handled, err := c.tryNonRemoteSignature(i, signerIdentity, verifierGetter, context, externalWallets, requestRaw)
		if err != nil {
			return nil, err
		}
		if handled {
			sigmas[signerIdentity.UniqueID()] = sigma

			continue
		}

		// Case: the signature must be generated by a remote party.
		// The same signer can back multiple actions; contact each party once,
		// so no two workers share the party's session.
		logger.DebugfContext(context.Context(), "no signer or wallet found for party [%s], request remote signature", signerIdentity)
		if !remoteSeen.Contains(signerIdentity.UniqueID()) {
			remoteSeen.Add(signerIdentity.UniqueID())
			remoteSigners = append(remoteSigners, signerIdentity)
		}
	}

	// Remote parties are independent, fan out their round-trips so total latency
	// tracks the slowest party instead of the sum. Workers share no state: the
	// sigmas map is populated here from the collected answers.
	if len(remoteSigners) > 0 {
		logger.DebugfContext(context.Context(), "request [%d] remote signatures concurrently", len(remoteSigners))
		remoteSigmas, err := fanOut(context.Context(), len(remoteSigners), AnswerCollectionTimeout, func(i int) ([]byte, error) {
			party := remoteSigners[i]
			sigma, err := c.signRemote(context, party, &SignatureRequest{TX: txRaw, Signer: party}, requestRaw, verifierGetter)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed signing remote for party [%s]", party)
			}

			return sigma, nil
		})
		if err != nil {
			return nil, err
		}
		for i, party := range remoteSigners {
			sigmas[party.UniqueID()] = remoteSigmas[i]
		}
	}
	logger.DebugfContext(context.Context(), "Done signing")

	return sigmas, nil
}

// signLocal generates a signature using a locally available signer for the given party.
// This is used when the signing key is available on the local node.
func (c *CollectEndorsementsView) signLocal(ctx context.Context, party view.Identity, signer token.Signer, requestRaw []byte) ([]byte, error) {
	logger.DebugfContext(ctx, "signing [request_hash=%s][tx_id=%s][nonce=%s]", utils.Hashable(requestRaw), c.tx.ID(), logging.Base64(c.tx.TxID.Nonce))

	sigma, err := signer.Sign(requestRaw)
	if err != nil {
		return nil, err
	}
	logger.DebugfContext(ctx, "signature generated (local, me) [%s,%s,%s,%v]", utils.Hashable(requestRaw), utils.Hashable(sigma), party, logging.Identifier(signer))

	return sigma, nil
}

// signExternal generates a signature using an external wallet signer for the given party.
// This is used when the wallet exists locally but the signing key is managed externally
// (e.g., hardware security module, remote signing service).
func (c *CollectEndorsementsView) signExternal(ctx context.Context, party view.Identity, signer ExternalWalletSigner, requestRaw []byte) ([]byte, error) {
	logger.DebugfContext(ctx, "signing [request=%s][tx_id=%s][nonce=%s]", utils.Hashable(requestRaw), c.tx.ID(), logging.Base64(c.tx.TxID.Nonce))
	sigma, err := signer.Sign(party, requestRaw)
	if err != nil {
		return nil, err
	}
	logger.DebugfContext(ctx, "signature generated (external, me) [%s,%s,%s]", utils.Hashable(requestRaw), utils.Hashable(sigma), party)

	return sigma, nil
}

// SigResponseTimeout bounds how long the initiator waits for one remote
// signer's reply within a signature fan-out phase.
const SigResponseTimeout = 1 * time.Minute

// signRemote requests a signature from a remote party by opening a network session,
// sending the signature request, receiving the signature, and verifying it.
// This is used when the signing party is on a different node.
func (c *CollectEndorsementsView) signRemote(
	context view.Context,
	party view.Identity,
	signatureRequest *SignatureRequest,
	requestRaw []byte,
	verifierGetter verifierGetterFunc,
) ([]byte, error) {
	session, err := context.GetSession(context.Initiator(), party)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting session")
	}
	ts := session2.NewTypedSession(context, session)
	if err := ts.SendTyped(context.Context(), signatureRequest, TypeSignatureRequest); err != nil {
		return nil, errors.Wrap(err, "failed sending transaction content")
	}

	var signaturePayload SignaturePayload
	if err := ts.ReceiveTypedWithTimeout(TypeSignature, &signaturePayload, SigResponseTimeout); err != nil {
		return nil, errors.Wrap(err, "failed reading message")
	}
	sigma := signaturePayload.Signature
	verifier, err := verifierGetter(context.Context(), party)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting verifier for [%s]", party)
	}
	logger.DebugfContext(context.Context(), "verify signature [%s][%s][%s] for txid [%s]", utils.Hashable(requestRaw), utils.Hashable(sigma), party, c.tx.ID())

	err = verifier.Verify(requestRaw, sigma)
	if err != nil {
		return nil, errors.Wrapf(err, "failed verifying signature [%s] from [%s]", sigma, party)
	}

	logger.DebugfContext(context.Context(), "signature verified [%s,%s,%s]", utils.Hashable(requestRaw), utils.Hashable(sigma), party)

	return sigma, nil
}

// requestApproval invokes the token chaincode to collect endorsements on the token request
// and prepare the transaction envelope. It marshals the token request, calls the network's
// RequestApproval method, and stores the resulting envelope in the transaction.
func (c *CollectEndorsementsView) requestApproval(context view.Context) (*network.Envelope, error) {
	requestRaw, err := c.tx.TokenRequest.RequestToBytes()
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling request")
	}

	logger.DebugfContext(context.Context(), "call chaincode for endorsement [nonce=%s]", logging.Base64(c.tx.TxID.Nonce))

	env, err := network.GetInstance(context, c.tx.Network(), c.tx.Channel()).RequestApproval(
		context,
		c.tx.TokenRequest.TokenService,
		requestRaw,
		c.tx.Signer,
		c.tx.TxID,
		c.Opts.ApprovalMetadata,
	)
	if err != nil {
		return nil, err
	}
	c.tx.Envelope = env

	return env, nil
}

// requestAudit requests auditing of the transaction from the configured auditor.
// If no auditor is specified in the transaction options but auditors are defined in
// the public parameters, a warning is logged. Returns the list of auditors that
// were contacted (empty if auditing was skipped).
func (c *CollectEndorsementsView) requestAudit(context view.Context) ([]view.Identity, error) {
	auditors := c.tx.TokenService().PublicParametersManager().PublicParameters().Auditors()
	logger.DebugfContext(context.Context(), "# auditors in public parameters [%d]", len(auditors))
	if len(c.tx.TokenService().PublicParametersManager().PublicParameters().Auditors()) == 0 {
		return nil, nil
	}

	if !c.tx.Opts.Auditor.IsNone() {
		logger.DebugfContext(context.Context(), "ask auditing to [%s]", c.tx.Opts.Auditor)
		sigService, err := sig.GetService(context)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting sig service for [%s]", c.tx.Opts.Auditor)
		}
		local := sigService.IsMe(context.Context(), c.tx.Opts.Auditor)
		sessionBoxed, err := context.RunView(newAuditingViewInitiator(c.tx, local, c.Opts.SkipAuditorSignatureVerification))
		if err != nil {
			return nil, errors.WithMessagef(err, "failed requesting auditing from [%s]", c.tx.Opts.Auditor.String())
		}
		c.sessionsMu.Lock()
		c.sessions[c.tx.Opts.Auditor.String()] = sessionBoxed.(view.Session)
		c.sessionsMu.Unlock()

		return []view.Identity{c.tx.Opts.Auditor}, nil
	} else {
		logger.Warnf("no auditor specified, skip auditing, but # auditors in public parameters is [%d]", len(auditors))
	}

	return nil, nil
}

// cleanupSessions closes and removes every session opened while collecting
// endorsements (e.g. the auditor session). Deleting entries as they are closed
// makes it idempotent, so the deferred call is safe to run on any return path.
func (c *CollectEndorsementsView) cleanupSessions(ctx context.Context) {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	for key, session := range c.sessions {
		delete(c.sessions, key)
		if session == nil {
			continue
		}
		logger.DebugfContext(ctx, "closing endorsement session [%s]", key)
		session.Close()
	}
}

// distributeTxToParties distributes the endorsed transaction to all parties in the distribution list.
// It filters metadata by enrollment ID for each recipient (except auditors who receive full metadata),
// stores transaction records locally, and collects acknowledgment signatures from each party.
//
//nolint:gocognit // distributes the finalized transaction to auditors and parties with different delivery rules per recipient kind; a split risks a recipient silently falling through the wrong path.
func (c *CollectEndorsementsView) distributeTxToParties(context view.Context, distributionList []view.Identity, auditors []view.Identity) error {
	logger.DebugfContext(context.Context(), "Start distribute to parties")
	if c.Opts.SkipDistributeEnv {
		logger.DebugfContext(context.Context(), "Skip distribute envelopes")

		return nil
	}

	if logger.IsEnabledFor(zapcore.DebugLevel) {
		if err := c.tx.IsValid(context.Context()); err != nil {
			return errors.Wrap(err, "failed verifying transaction content before distributing it")
		}
	}

	// Distribute the transaction to all parties in the distribution list.
	// Filter the metadata by Enrollment ID.
	// The auditor will receive the full set of metadata
	finalDistributionList, err := c.prepareDistributionList(context, auditors, distributionList)
	if err != nil {
		return errors.Wrap(err, "failed preparing distribution list")
	}

	owner := NewOwner(context, c.tx.TokenService())

	// Store transaction in the token transaction database
	logger.DebugfContext(context.Context(), "Store transaction records")
	if err := StoreTransactionRecords(context, c.tx); err != nil {
		return errors.Wrapf(err, "failed adding transaction %s to the token transaction database", c.tx.ID())
	}

	// Marshal the payload for each remote party on this goroutine.
	remote, payloads, err := c.marshalPayloads(context.Context(), finalDistributionList)
	if err != nil {
		return err
	}

	// TODO:
	// This operation might be retried, but this requires a change of protocol to make sure the recipient can always receive.
	// It could be done by using a new context.
	// Remote parties are independent, fan out the round-trips so total latency
	// tracks the slowest party. Workers only do network I/O and verification;
	// the acks are recorded on this goroutine from the collected answers.
	sigmas, err := fanOut(context.Context(), len(remote), AnswerCollectionTimeout, func(i int) ([]byte, error) {
		entry := &remote[i]
		logger.DebugfContext(context.Context(), "Distribute to %v", entry.EIDs)
		sigma, err := c.distributeTxToParty(context, entry, payloads[i])
		if err != nil {
			return nil, errors.Wrapf(err, "failed distribute evn of tx [%s] to party [%v:%s]", c.tx.ID(), entry.EIDs, entry.ID)
		}
		logger.DebugfContext(context.Context(), "Done distributing to %v", entry.EIDs)

		return sigma, nil
	})
	if err != nil {
		return err
	}
	for i := range remote {
		if err := owner.appendTransactionEndorseAck(context.Context(), c.tx, remote[i].LongTerm, sigmas[i]); err != nil {
			return errors.Wrapf(err, "failed appending transaction endorsement ack to transaction %s", c.tx.ID())
		}
	}

	return nil
}

// marshalPayloads returns the remote entries of distributionList and the payload to send to each.
// An auditor receives the full set of metadata.
// Any other party receives the metadata filtered by its enrollment IDs.
func (c *CollectEndorsementsView) marshalPayloads(ctx context.Context, distributionList []distributionListEntry) ([]distributionListEntry, [][]byte, error) {
	logger.DebugfContext(ctx, "start distributing to %d parties", len(distributionList))
	remote := make([]distributionListEntry, 0, len(distributionList))
	payloads := make([][]byte, 0, len(distributionList))
	for i, entry := range distributionList {
		// If it is me, no need to open a remote connection. Just store the envelope locally.
		if entry.IsMe && !entry.Auditor {
			logger.DebugfContext(ctx, "tx [%d] is me [%s], endorse locally", i, entry.ID)

			continue
		}
		logger.DebugfContext(ctx, "tx [%d] is not me [%s:%v], ask endorse", i, entry.ID, entry.EIDs)

		var txRaw []byte
		var err error
		if entry.Auditor {
			logger.DebugfContext(ctx, "This is an auditor [%s], send the full set of metadata", entry.ID)
			txRaw, err = c.tx.Bytes(ctx)
		} else {
			logger.DebugfContext(ctx, "This is not an auditor [%s], send the filtered metadata", entry.ID)
			// Bytes with no enrollment ID returns the full metadata: never send it to a non-auditor.
			if len(entry.EIDs) == 0 {
				return nil, nil, errors.Errorf("no enrollment ID for party [%s]", entry.ID)
			}
			txRaw, err = c.tx.Bytes(ctx, entry.EIDs...)
		}
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed marshalling transaction content")
		}
		remote = append(remote, entry)
		payloads = append(payloads, txRaw)
	}

	return remote, payloads, nil
}

// distributeTxToParty sends the transaction to a single party, waits for their acknowledgment,
// verifies the acknowledgment signature, and returns it. It is safe to call from
// concurrent goroutines: it writes no shared state.
// The txRaw parameter should contain the transaction bytes filtered by the party's enrollment ID
// (unless the party is an auditor, in which case it contains the full transaction).
func (c *CollectEndorsementsView) distributeTxToParty(
	context view.Context,
	entry *distributionListEntry,
	txRaw []byte,
) ([]byte, error) {
	// Open a session to the party. and send the transaction.
	session, err := c.getSession(context, entry.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting session")
	}
	// Send the content
	logger.DebugfContext(context.Context(), "Send transaction content")
	ts := session2.NewTypedSession(context, session)
	if err := ts.SendTyped(context.Context(), &TransactionPayload{Raw: txRaw}, TypeTransaction); err != nil {
		return nil, errors.Wrap(err, "failed sending transaction content")
	}

	logger.DebugfContext(context.Context(), "Wait for ack")
	var signaturePayload SignaturePayload
	if err := ts.ReceiveTypedWithTimeout(TypeSignature, &signaturePayload, time.Minute); err != nil {
		return nil, errors.Wrapf(err, "failed reading message on session [%s]", session.Info().ID)
	}
	sigma := signaturePayload.Signature
	logger.DebugfContext(context.Context(), "received ack from [%s] [%s], checking signature on [%s]",
		entry.LongTerm, utils.Hashable(sigma).String(),
		utils.Hashable(txRaw).String())

	logger.DebugfContext(context.Context(), "Verify signature")
	sigService, err := sig.GetService(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting sig service for [%s]", c.tx.Opts.Auditor)
	}
	verifier, err := sigService.GetVerifier(entry.LongTerm)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting verifier for identity [%s]", entry.ID)
	}
	if err := verifier.Verify(txRaw, sigma); err != nil {
		return nil, errors.Wrapf(err, "failed verifying ack signature from [%s]", entry.ID)
	}

	logger.DebugfContext(context.Context(), "CollectEndorsementsView: collected signature from %s", entry.ID)

	return sigma, nil
}

// prepareDistributionList processes the raw distribution list and auditors list to create
// a compressed list of unique parties to distribute the transaction to. It:
// - Unwraps multi-signature and policy identities into their component identities
// - Resolves long-term identities for remote parties
// - Extracts enrollment IDs for non-local parties
// - Removes duplicates based on long-term identity
// - Marks which parties are local (isMe) and which are auditors
// Returns a deduplicated list of distribution entries with all necessary metadata.
// unwrapDistributionIDs expands any multi-sig or policy identities in distributionList into
// their component identities, dropping none (redeem) identities.
func unwrapDistributionIDs(distributionList []view.Identity) ([]view.Identity, error) {
	allIds := make([]view.Identity, 0, len(distributionList))
	for _, id := range distributionList {
		if id.IsNone() {
			// This is a redeem, nothing to do here.
			continue
		}
		multiSigners, ok, err := multisig.Unwrap(id)
		if err != nil {
			return nil, errors.Wrapf(err, "failed unwrapping multi-sig identity [%s]", id)
		}
		if ok {
			allIds = append(allIds, multiSigners...)

			continue
		}

		pi, ok, err := boolpolicy.Unwrap(id)
		if err != nil {
			return nil, errors.Wrapf(err, "failed unwrapping policy identity [%s]", id)
		}
		if ok {
			for _, b := range pi.Identities {
				allIds = append(allIds, token.Identity(b))
			}

			continue
		}

		allIds = append(allIds, id)
	}

	return allIds, nil
}

// resolveLongTermIdentity returns party's long-term identity: the node's own default identity
// if isMe, otherwise the identity resolved via the endpoint service.
func resolveLongTermIdentity(context view.Context, party view.Identity, isMe bool) (view.Identity, error) {
	// if it is me, no need to resolve, get directly the default identity
	if isMe {
		idProvider, err := id.GetProvider(context)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting identity provider")
		}

		return idProvider.DefaultIdentity(), nil
	}

	longTermIdentity, _, _, err := endpoint.GetService(context).Resolve(context.Context(), party)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot resolve long term identity for [%s]", party.UniqueID())
	}

	return longTermIdentity, nil
}

// addPartyToDistributionList resolves party's long-term identity and, if not already present in
// distributionListCompressed, appends a new (non-auditor) entry for it.
// If a remote non-auditor entry has the same long-term identity, it adds party's enrollment ID to that entry.
// auditInfos holds the audit infos carried in the token request, as returned by requestAuditInfos.
func (c *CollectEndorsementsView) addPartyToDistributionList(
	context view.Context,
	party view.Identity,
	mine collections.Set[string],
	auditInfos map[string][][]byte,
	distributionListCompressed []distributionListEntry,
) ([]distributionListEntry, error) {
	// For each party in the distribution list:
	// - check if it is me
	// - check if it is an auditor
	// - extract the corresponding long term identity
	// If the long term identity has not been added yet, add it to the list.
	// If the party is me or an auditor, no need to extract the enrollment ID.
	logger.DebugfContext(context.Context(), "distribute tx to [%s]?", party)

	isMe := mine.Contains(party.UniqueID())
	if !isMe {
		// check if there is a wallet that contains that identity
		_, err := c.tx.TokenService().WalletManager().OwnerWallet(context.Context(), party)
		isMe = err == nil
	}
	logger.DebugfContext(context.Context(), "distribute tx to [%s], it is me [%v].", party, isMe)

	longTermIdentity, err := resolveLongTermIdentity(context, party, isMe)
	if err != nil {
		return nil, err
	}
	logger.DebugfContext(context.Context(), "searching for long term identity [%s]", longTermIdentity)

	for i := range distributionListCompressed {
		entry := &distributionListCompressed[i]
		if !longTermIdentity.Equal(entry.LongTerm) {
			continue
		}
		// The same remote node can host owners with different enrollment IDs:
		// it must receive the metadata of all of them.
		if isMe || entry.IsMe || entry.Auditor {
			logger.DebugfContext(context.Context(), "skip adding [%s] to distribution list, already added", party)

			return distributionListCompressed, nil
		}
		eID, err := c.partyEnrollmentID(context.Context(), party, auditInfos)
		if err != nil {
			// The node is already in the list, as before #2713.
			logger.DebugfContext(context.Context(), "skip adding [%s] to distribution list, already added, no enrollment ID: %s", party, err)

			return distributionListCompressed, nil
		}
		if !slices.Contains(entry.EIDs, eID) {
			logger.DebugfContext(context.Context(), "adding enrollment ID of [%s] to distribution list entry [%s]", party, entry.ID)
			entry.EIDs = append(entry.EIDs, eID)
		}

		return distributionListCompressed, nil
	}

	var eIDs []string
	if !isMe {
		eID, err := c.partyEnrollmentID(context.Context(), party, auditInfos)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting enrollment ID for [%s]", party.UniqueID())
		}
		eIDs = []string{eID}
	}

	logger.DebugfContext(context.Context(), "adding [%s] to distribution list", party)

	return append(distributionListCompressed, distributionListEntry{
		IsMe:     isMe,
		LongTerm: longTermIdentity,
		ID:       party,
		EIDs:     eIDs,
		Auditor:  false,
	}), nil
}

// partyEnrollmentID returns the enrollment ID of party.
// It reads the local audit info of party first.
// If that gives no enrollment ID, it tries each audit info in auditInfos for party, in order.
// It uses an audit info only if it matches party, because the request comes from other nodes.
// It never returns an empty enrollment ID without an error.
func (c *CollectEndorsementsView) partyEnrollmentID(ctx context.Context, party view.Identity, auditInfos map[string][][]byte) (string, error) {
	tms := c.tx.TokenService()
	eID, err := tms.WalletManager().GetEnrollmentID(ctx, party)
	if err == nil && len(eID) != 0 {
		return eID, nil
	}
	if err == nil {
		err = errors.Errorf("empty enrollment ID in local audit info of [%s]", party.UniqueID())
	}
	errs := []error{err}
	for _, auditInfo := range auditInfos[party.UniqueID()] {
		if err := tms.SigService().MatchIdentity(ctx, party, auditInfo); err != nil {
			errs = append(errs, errors.WithMessagef(err, "request audit info does not match [%s]", party.UniqueID()))

			continue
		}
		eID, _, err := tms.WalletManager().GetEIDAndRH(ctx, party, auditInfo)
		if err != nil {
			errs = append(errs, errors.WithMessagef(err, "failed reading request audit info of [%s]", party.UniqueID()))

			continue
		}
		if len(eID) == 0 {
			errs = append(errs, errors.Errorf("empty enrollment ID in request audit info of [%s]", party.UniqueID()))

			continue
		}

		return eID, nil
	}

	return "", errors.Join(errs...)
}

// requestAuditInfos maps the UniqueID of each issuer, sender and receiver in meta to the
// non-empty audit infos that meta carries for it, in order and without duplicates.
// Multisig and policy identities are replaced by their members, as in unwrapDistributionIDs,
// each with its own audit info.
// The audit infos are not verified: match them against the identity before use.
func requestAuditInfos(ctx context.Context, meta *driver.TokenRequestMetadata) map[string][][]byte {
	index := map[string][][]byte{}
	if meta == nil {
		return index
	}
	for _, action := range meta.Actions {
		if action == nil {
			continue
		}
		if issue := action.IssueMetadata; issue != nil {
			indexAuditInfo(ctx, index, &issue.Issuer)
			for _, output := range issue.Outputs {
				if output != nil {
					for _, receiver := range output.Receivers {
						indexAuditInfo(ctx, index, receiver)
					}
				}
			}
		}
		if transfer := action.TransferMetadata; transfer != nil {
			for _, input := range transfer.Inputs {
				if input != nil {
					for _, sender := range input.Senders {
						indexAuditInfo(ctx, index, sender)
					}
				}
			}
			for _, output := range transfer.Outputs {
				if output != nil {
					for _, receiver := range output.Receivers {
						indexAuditInfo(ctx, index, receiver)
					}
				}
			}
		}
	}

	return index
}

// indexAuditInfo adds the audit info of party to index.
// For a multisig or policy identity, it adds each member with its own audit info.
// It skips the members if the audit info does not decode or has another member count.
func indexAuditInfo(ctx context.Context, index map[string][][]byte, party *driver.AuditableIdentity) {
	if party == nil || party.Identity.IsNone() || len(party.AuditInfo) == 0 {
		return
	}
	members, unwrapAuditInfo := unwrapComposite(party.Identity)
	if members == nil {
		appendAuditInfo(index, party.Identity, party.AuditInfo)

		return
	}
	_, memberAuditInfos, err := unwrapAuditInfo(party.AuditInfo)
	if err != nil || len(memberAuditInfos) != len(members) {
		logger.DebugfContext(ctx, "skip audit info of composite identity [%s]: members [%d], audit infos [%d], err [%v]", party.Identity, len(members), len(memberAuditInfos), err)

		return
	}
	for i, member := range members {
		if member.IsNone() || len(memberAuditInfos[i]) == 0 {
			continue
		}
		appendAuditInfo(index, member, memberAuditInfos[i])
	}
}

// unwrapComposite returns the members of a multisig or policy identity and the function that
// unwraps its audit info. It returns nil members for any other identity.
func unwrapComposite(id view.Identity) ([]view.Identity, func([]byte) (bool, [][]byte, error)) {
	if members, ok, err := multisig.Unwrap(id); err == nil && ok {
		return members, multisig.UnwrapAuditInfo
	}
	if pi, ok, err := boolpolicy.Unwrap(id); err == nil && ok {
		members := make([]view.Identity, len(pi.Identities))
		for i, b := range pi.Identities {
			members[i] = token.Identity(b)
		}

		return members, boolpolicy.UnwrapAuditInfo
	}

	return nil, nil
}

// appendAuditInfo appends auditInfo to the audit infos of id in index, unless it is already there.
func appendAuditInfo(index map[string][][]byte, id view.Identity, auditInfo []byte) {
	key := id.UniqueID()
	for _, existing := range index[key] {
		if slices.Equal(existing, auditInfo) {
			return
		}
	}
	index[key] = append(index[key], auditInfo)
}

// addAuditorToDistributionList resolves party's long-term identity and appends an auditor entry
// for it to distributionListCompressed.
func addAuditorToDistributionList(context view.Context, party view.Identity, mine collections.Set[string], distributionListCompressed []distributionListEntry) ([]distributionListEntry, error) {
	isMe := mine.Contains(party.UniqueID())
	logger.DebugfContext(context.Context(), "distribute tx to auditor [%s], it is me [%v].", party, isMe)

	longTermIdentity, err := resolveLongTermIdentity(context, party, isMe)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot resolve long term auditor identity for [%s]", party.UniqueID())
	}

	return append(distributionListCompressed, distributionListEntry{
		IsMe:     isMe,
		ID:       party,
		Auditor:  true,
		LongTerm: longTermIdentity,
	}), nil
}

func (c *CollectEndorsementsView) prepareDistributionList(context view.Context, auditors []view.Identity, distributionList []view.Identity) ([]distributionListEntry, error) {
	// Compress distributionList by removing duplicates

	// check if there are multisig identities, if yes, unwrap them
	unwrapped, err := unwrapDistributionIDs(distributionList)
	if err != nil {
		return nil, err
	}
	distributionList = unwrapped
	allIds := append(append([]view.Identity{}, unwrapped...), auditors...)

	sigService, err := sig.GetService(context)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting sig service for [%s]", c.tx.Opts.Auditor)
	}
	mine := collections.NewSet(sigService.AreMe(context.Context(), allIds...)...)
	remainingIds := make([]view.Identity, 0, len(allIds)-mine.Length())
	for _, id := range allIds {
		if !mine.Contains(id.UniqueID()) {
			remainingIds = append(remainingIds, id)
		}
	}
	remainingMine, err := c.tx.TokenService().SigService().AreMe(context.Context(), remainingIds...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed checking which of the remaining [%d] identities are me", len(remainingIds))
	}
	mine.Add(remainingMine...)
	logger.DebugfContext(context.Context(), "%d/%d ids were mine", mine.Length(), len(allIds))

	var meta *driver.TokenRequestMetadata
	if c.tx.Payload != nil && c.tx.TokenRequest != nil {
		meta = c.tx.TokenRequest.Metadata
	}
	auditInfos := requestAuditInfos(context.Context(), meta)

	var distributionListCompressed []distributionListEntry
	for _, party := range distributionList {
		distributionListCompressed, err = c.addPartyToDistributionList(context, party, mine, auditInfos, distributionListCompressed)
		if err != nil {
			return nil, err
		}
	}

	// check the auditors
	for _, party := range auditors {
		distributionListCompressed, err = addAuditorToDistributionList(context, party, mine, distributionListCompressed)
		if err != nil {
			return nil, err
		}
	}

	logger.DebugfContext(context.Context(), "distributed tx to num parties [%d]", len(distributionListCompressed))

	return distributionListCompressed, nil
}

// getSession retrieves an existing session for the given party from the cache,
// or creates a new session if one doesn't exist. It is called by the concurrent
// distribution workers.
func (c *CollectEndorsementsView) getSession(context view.Context, p view.Identity) (view.Session, error) {
	c.sessionsMu.Lock()
	s, ok := c.sessions[p.UniqueID()]
	c.sessionsMu.Unlock()
	if ok {
		logger.DebugfContext(context.Context(), "getSession: found session for [%s]", p.UniqueID())

		return s, nil
	}

	return context.GetSession(context.Initiator(), p)
}

// AnswerCollectionTimeout bounds a whole signature fan-out phase, not a single
// answer. It is the phase's true upper bound rather than a backstop: a worker's
// own SigResponseTimeout covers only the receive, not the session dial or the
// signature verification around it.
const AnswerCollectionTimeout = 2 * time.Minute

// noPerAnswerTimeout disables the collector's own per-Collect timer, so a fan-out
// is bounded only by the context deadline fanOut derives. A collector timeout of
// zero would select DefaultAnswersCollectorTimeout instead of disabling the timer.
const noPerAnswerTimeout = time.Duration(math.MaxInt64)

// fanOut runs work(i) for each i in [0, n) on its own goroutine and returns the
// results in index order. It returns on the first error without waiting for the
// remaining workers; those drain into the collector's buffered channel and exit.
//
// timeout bounds the fan-out as a whole: it is applied to the context, and the
// collector is given no timer of its own, so the context deadline is the single
// source of truth. The collector arms a fresh timer on each Collect call, which
// on its own would let a phase with n workers run for n times the timeout.
func fanOut[T any](ctx context.Context, n int, timeout time.Duration, work func(i int) (T, error)) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	collector := utils.NewAnswersCollector[int, T](n, noPerAnswerTimeout)
	for i := range n {
		go func() {
			value, err := work(i)
			collector.Send(i, value, err)
		}()
	}

	results := make([]T, n)
	for range n {
		answers, err := collector.Collect(ctx, 1)
		if err != nil {
			return nil, err
		}
		if answers[0].Err != nil {
			return nil, answers[0].Err
		}
		results[answers[0].Key] = answers[0].Value
	}

	return results, nil
}

// mergeSigmas merges multiple signature maps into a single map.
// If the same key appears in multiple maps, the last occurrence wins.
func mergeSigmas(maps ...map[string][]byte) map[string][]byte {
	merged := make(map[string][]byte)
	for _, m := range maps {
		maps0.Copy(merged, m)
	}

	return merged
}

// IssueDistributionList extracts all parties involved in issue operations from the token request.
// Returns a list containing all issuers and receivers from all issue actions.
func IssueDistributionList(r *token.Request) []view.Identity {
	distributionList := make([]view.Identity, 0)
	for _, issue := range r.Issues() {
		distributionList = append(distributionList, issue.Issuer)
		distributionList = append(distributionList, issue.Receivers...)
	}

	return distributionList
}

// TransferDistributionList extracts all parties involved in transfer operations from the token request.
// Returns a list containing all senders and receivers from all transfer actions.
func TransferDistributionList(r *token.Request) []view.Identity {
	distributionList := make([]view.Identity, 0)
	for _, transfer := range r.Transfers() {
		distributionList = append(distributionList, transfer.Senders...)
		distributionList = append(distributionList, transfer.Receivers...)
	}

	return distributionList
}

// policyCollectIDs returns the subset of componentIDs to collect signatures from.
// When WithPolicySigners was supplied, only those matching identities are returned;
// otherwise all components are returned (the default, AND-safe behaviour).
func (c *CollectEndorsementsView) policyCollectIDs(componentIDs []token.Identity) []token.Identity {
	if len(c.Opts.PolicySigners) == 0 {
		return componentIDs
	}
	allowed := make(map[string]struct{}, len(c.Opts.PolicySigners))
	for _, id := range c.Opts.PolicySigners {
		allowed[id.UniqueID()] = struct{}{}
	}
	filtered := make([]token.Identity, 0, len(c.Opts.PolicySigners))
	for _, id := range componentIDs {
		if _, ok := allowed[id.UniqueID()]; ok {
			filtered = append(filtered, id)
		}
	}

	return filtered
}

// CleanupExternalWallets calls Done() on all external wallets to signal completion
func (c *CollectEndorsementsView) CleanupExternalWallets(context view.Context, externalWallets map[string]ExternalWalletSigner) {
	logger.DebugfContext(context.Context(), "Inform external wallets that endorsement is complete")
	for id, signer := range externalWallets {
		if err := signer.Done(); err != nil {
			logger.ErrorfContext(context.Context(), "failed to signal done external wallet [%s], error: %+v", id, err)
		}
	}
}
