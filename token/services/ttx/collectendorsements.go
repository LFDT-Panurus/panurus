/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/json"
	maps0 "maps"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
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
	EID      string
	Auditor  bool
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
	tx       *Transaction
	Opts     *EndorsementsOpts
	sessions map[string]view.Session
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
	if !c.tx.TokenRequest.SetSignatures(mergeSigmas(issueSigmas, transferSigmas)) {
		return nil, errors.New("failed setting signatures on token request, some signatures are missing")
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

	// Cleanup audit
	logger.DebugfContext(context.Context(), "Cleanup audit")
	if err := c.cleanupAudit(context); err != nil {
		logger.ErrorfContext(context.Context(), "failed cleaning up audit: %s", err)

		return nil, errors.WithMessagef(err, "failed cleaning up audit")
	}

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

	// Use IssueSigners() - the action context is preserved in metadata and used by SetSignatures()
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

	// Use TransferSigners() - the action context is preserved in metadata and used by SetSignatures()
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
// - Remote signers: requests signatures from remote parties via network sessions
// Returns a map of signer identity unique IDs to their signatures.
func (c *CollectEndorsementsView) requestSignatures(signers []view.Identity, verifierGetter verifierGetterFunc, context view.Context, externalWallets map[string]ExternalWalletSigner) (map[string][]byte, error) {
	logger.DebugfContext(context.Context(), "Request %d signatures", len(signers))
	requestRaw, err := c.tx.TokenRequest.MarshalToSign()
	if err != nil {
		return nil, err
	}
	txRaw, err := c.tx.Bytes()
	if err != nil {
		return nil, err
	}

	sigmas := make(map[string][]byte)
	for i, signerIdentity := range signers {
		// we have the following possibilities:
		// - there is a signer locally bound to the party, use it to generate the signature
		// - there is a wallet bound to the party but the signer is not local, the signature is generated externally
		// - the identity is a multi-sig identity
		// - the signature must be generated by a remote party

		signatureRequest := &SignatureRequest{
			TX:     txRaw,
			Signer: signerIdentity,
		}
		logger.DebugfContext(context.Context(), "collecting signature [%d] on request from [%s]", i, signerIdentity)

		// Case: the identity is a multi-sig identity
		multiSigners, ok, err := multisig.Unwrap(signerIdentity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed unwrapping multi-sig identity [%s]", signerIdentity)
		}
		if ok {
			logger.DebugfContext(context.Context(), "found multi-sig identity [%s], request multi-sig signature to [%d] parties", signerIdentity, len(multiSigners))
			// collect the signatures from multiSigners
			multiSignersSigmas, err := c.requestSignatures(multiSigners, verifierGetter, context, externalWallets)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed requesting signatures")
			}
			logger.DebugfContext(context.Context(), "collected [%d] signatures for multi-sig identity [%s]", len(multiSignersSigmas), signerIdentity)
			sigma, err := multisig.JoinSignatures(multiSigners, multiSignersSigmas)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed joining multi-sig signatures")
			}
			sigmas[signerIdentity.UniqueID()] = sigma

			continue
		}

		// Case: the identity is a policy identity
		pi, ok, err := boolpolicy.Unwrap(signerIdentity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed unwrapping policy identity [%s]", signerIdentity)
		}
		if ok {
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
				return nil, errors.WithMessagef(err, "failed requesting policy signatures")
			}
			sigma, err := boolpolicy.JoinSignatures(componentIDs, componentSigmas)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed joining policy signatures")
			}
			sigmas[signerIdentity.UniqueID()] = sigma

			continue
		}

		// Case: there is a wallet bound to the party with an external signer registered,
		// the signature is generated externally. Probing the wallet first lets callers
		// who have explicitly registered an ExternalWalletSigner short-circuit in O(1),
		// avoiding the x509 parse + BCCSP key load that GetSigner runs for identities
		// whose private key is held outside the local BCCSP (e.g. in an external KMS).
		if w, err := c.tx.TokenService().WalletManager().OwnerWallet(context.Context(), signerIdentity); err == nil {
			if ews := c.Opts.ExternalWalletSigner(w.ID()); ews != nil {
				logger.DebugfContext(context.Context(), "found wallet for party [%s], request external signature", signerIdentity)
				externalWallets[w.ID()] = ews
				sigma, err := c.signExternal(context.Context(), signerIdentity, ews, requestRaw)
				if err != nil {
					return nil, errors.WithMessagef(err, "failed signing external for party [%s]", signerIdentity)
				}
				sigmas[signerIdentity.UniqueID()] = sigma

				continue
			}
			// wallet exists but no ExternalWalletSigner registered; fall through to the
			// local-signer path. ExternalWalletSigner registration is an explicit opt-in,
			// not a strict requirement.
		}

		// Case: there is a signer locally bound to the party, use it to generate the signature
		if signer, err := c.tx.TokenService().SigService().GetSigner(context.Context(), signerIdentity); err == nil {
			logger.DebugfContext(context.Context(), "found signer for party [%s], request local signature", signerIdentity)
			sigma, err := c.signLocal(context.Context(), signerIdentity, signer, requestRaw)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed signing local for party [%s]", signerIdentity)
			}
			sigmas[signerIdentity.UniqueID()] = sigma

			continue
		} else {
			logger.DebugfContext(context.Context(), "failed to find a signer for party [%s]: [%s]", signerIdentity, err)
		}

		// Case: the signature must be generated by a remote party
		logger.DebugfContext(context.Context(), "no signer or wallet found for party [%s], request remote signature", signerIdentity)
		sigma, err := c.signRemote(context, signerIdentity, signatureRequest, requestRaw, verifierGetter)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed signing remote for party [%s]", signerIdentity)
		}
		sigmas[signerIdentity.UniqueID()] = sigma
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
	if err := ts.ReceiveTypedWithTimeout(TypeSignature, &signaturePayload, time.Minute); err != nil {
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
		c.sessions[c.tx.Opts.Auditor.String()] = sessionBoxed.(view.Session)

		return []view.Identity{c.tx.Opts.Auditor}, nil
	} else {
		logger.Warnf("no auditor specified, skip auditing, but # auditors in public parameters is [%d]", len(auditors))
	}

	return nil, nil
}

// cleanupAudit closes the auditor session if one was opened during the audit process.
// This should be called after the transaction has been fully endorsed and distributed.
func (c *CollectEndorsementsView) cleanupAudit(context view.Context) error {
	if !c.tx.Opts.Auditor.IsNone() {
		session, err := c.getSession(context, c.tx.Opts.Auditor)
		if err != nil {
			return errors.Wrap(err, "failed getting auditor's session")
		}
		session.Close()
	}

	return nil
}

// distributeTxToParties distributes the endorsed transaction to all parties in the distribution list.
// It filters metadata by enrollment ID for each recipient (except auditors who receive full metadata),
// stores transaction records locally, and collects acknowledgment signatures from each party.
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

	logger.DebugfContext(context.Context(), "start distributing to %d parties", len(finalDistributionList))
	for i, entry := range finalDistributionList {
		// If it is me, no need to open a remote connection. Just store the envelope locally.
		if entry.IsMe && !entry.Auditor {
			logger.DebugfContext(context.Context(), "tx [%d] is me [%s], endorse locally", i, entry.ID)

			continue
		} else {
			logger.DebugfContext(context.Context(), "tx [%d] is not me [%s:%s], ask endorse", i, entry.ID, entry.EID)
		}

		// The party is not me, open a connection to the party.
		// If the party is an auditor, then send the full set of metadata.
		// Otherwise, filter the metadata by Enrollment ID.
		var txRaw []byte
		var err error
		if entry.Auditor {
			logger.DebugfContext(context.Context(), "This is an auditor [%s], send the full set of metadata", entry.ID)
			txRaw, err = c.tx.Bytes()
			if err != nil {
				return errors.Wrap(err, "failed marshalling transaction content")
			}
		} else {
			logger.DebugfContext(context.Context(), "This is not an auditor [%s], send the filtered metadata", entry.ID)
			txRaw, err = c.tx.Bytes(entry.EID)
			if err != nil {
				return errors.Wrap(err, "failed marshalling transaction content")
			}
		}

		// TODO:
		// This operation might be retried, but this requires a change of protocol to make sure the recipient can always receive.
		// It could be done by using a new context.
		logger.DebugfContext(context.Context(), "Distribute to %s", entry.EID)
		if err := c.distributeTxToParty(context, &entry, txRaw, owner); err != nil {
			return errors.Wrapf(err, "failed distribute evn of tx [%s] to party [%s:%s]", c.tx.ID(), entry.EID, entry.ID)
		}
		logger.DebugfContext(context.Context(), "Done distributing to %s", entry.EID)
	}

	return nil
}

// distributeTxToParty sends the transaction to a single party, waits for their acknowledgment,
// verifies the acknowledgment signature, and records it in the transaction database.
// The txRaw parameter should contain the transaction bytes filtered by the party's enrollment ID
// (unless the party is an auditor, in which case it contains the full transaction).
func (c *CollectEndorsementsView) distributeTxToParty(
	context view.Context,
	entry *distributionListEntry,
	txRaw []byte,
	owner *TxOwner,
) error {
	// Open a session to the party. and send the transaction.
	session, err := c.getSession(context, entry.ID)
	if err != nil {
		return errors.Wrap(err, "failed getting session")
	}
	// Send the content
	logger.DebugfContext(context.Context(), "Send transaction content")
	ts := session2.NewTypedSession(context, session)
	if err := ts.SendTyped(context.Context(), &TransactionPayload{Raw: txRaw}, TypeTransaction); err != nil {
		return errors.Wrap(err, "failed sending transaction content")
	}

	logger.DebugfContext(context.Context(), "Wait for ack")
	var signaturePayload SignaturePayload
	if err := ts.ReceiveTypedWithTimeout(TypeSignature, &signaturePayload, time.Minute); err != nil {
		return errors.Wrapf(err, "failed reading message on session [%s]", session.Info().ID)
	}
	sigma := signaturePayload.Signature
	logger.DebugfContext(context.Context(), "received ack from [%s] [%s], checking signature on [%s]",
		entry.LongTerm, utils.Hashable(sigma).String(),
		utils.Hashable(txRaw).String())

	logger.DebugfContext(context.Context(), "Verify signature")
	sigService, err := sig.GetService(context)
	if err != nil {
		return errors.Wrapf(err, "failed getting sig service for [%s]", c.tx.Opts.Auditor)
	}
	verifier, err := sigService.GetVerifier(entry.LongTerm)
	if err != nil {
		return errors.Wrapf(err, "failed getting verifier for identity [%s]", entry.ID)
	}
	if err := verifier.Verify(txRaw, sigma); err != nil {
		return errors.Wrapf(err, "failed verifying ack signature from [%s]", entry.ID)
	}

	logger.DebugfContext(context.Context(), "CollectEndorsementsView: collected signature from %s", entry.ID)

	if err := owner.appendTransactionEndorseAck(context.Context(), c.tx, entry.LongTerm, sigma); err != nil {
		return errors.Wrapf(err, "failed appending transaction endorsement ack to transaction %s", c.tx.ID())
	}

	return nil
}

// prepareDistributionList processes the raw distribution list and auditors list to create
// a compressed list of unique parties to distribute the transaction to. It:
// - Unwraps multi-signature and policy identities into their component identities
// - Resolves long-term identities for remote parties
// - Extracts enrollment IDs for non-local parties
// - Removes duplicates based on long-term identity
// - Marks which parties are local (isMe) and which are auditors
// Returns a deduplicated list of distribution entries with all necessary metadata.
func (c *CollectEndorsementsView) prepareDistributionList(context view.Context, auditors []view.Identity, distributionList []view.Identity) ([]distributionListEntry, error) {
	// Compress distributionList by removing duplicates

	// check if there are multisig identities, if yes, unwrap them
	allIds := make([]view.Identity, 0, len(distributionList)+len(auditors))
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
	distributionList = allIds
	allIds = append(allIds, auditors...)

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
	mine.Add(c.tx.TokenService().SigService().AreMe(context.Context(), remainingIds...)...)
	logger.DebugfContext(context.Context(), "%d/%d ids were mine", mine.Length(), len(allIds))

	var distributionListCompressed []distributionListEntry
	for _, party := range distributionList {
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
			_, err = c.tx.TokenService().WalletManager().OwnerWallet(context.Context(), party)
			isMe = err == nil
		}
		logger.DebugfContext(context.Context(), "distribute tx to [%s], it is me [%v].", party, isMe)
		var longTermIdentity view.Identity
		var err error
		// if it is me, no need to resolve, get directly the default identity
		if isMe {
			idProvider, err := id.GetProvider(context)
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting identity provider")
			}
			longTermIdentity = idProvider.DefaultIdentity()
		} else {
			longTermIdentity, _, _, err = endpoint.GetService(context).Resolve(context.Context(), party)
			if err != nil {
				return nil, errors.Wrapf(err, "cannot resolve long term identity for [%s]", party.UniqueID())
			}
		}
		logger.DebugfContext(context.Context(), "searching for long term identity [%s]", longTermIdentity)
		found := false
		for _, entry := range distributionListCompressed {
			if longTermIdentity.Equal(entry.LongTerm) {
				found = true

				break
			}
		}
		if !found {
			logger.DebugfContext(context.Context(), "adding [%s] to distribution list", party)
			eID := ""
			if !isMe {
				eID, err = c.tx.TokenService().WalletManager().GetEnrollmentID(context.Context(), party)
				if err != nil {
					return nil, errors.Wrapf(err, "failed getting enrollment ID for [%s]", party.UniqueID())
				}
			}
			distributionListCompressed = append(distributionListCompressed, distributionListEntry{
				IsMe:     isMe,
				LongTerm: longTermIdentity,
				ID:       party,
				EID:      eID,
				Auditor:  false,
			})
		} else {
			logger.DebugfContext(context.Context(), "skip adding [%s] to distribution list, already added", party)
		}
	}

	// check the auditors
	for _, party := range auditors {
		isMe := mine.Contains(party.UniqueID())
		logger.DebugfContext(context.Context(), "distribute tx to auditor [%s], it is me [%v].", party, isMe)
		var longTermIdentity view.Identity
		var err error
		// if it is me, no need to resolve, get directly the default identity
		if isMe {
			idProvider, err := id.GetProvider(context)
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting identity provider")
			}
			longTermIdentity = idProvider.DefaultIdentity()
		} else {
			longTermIdentity, _, _, err = endpoint.GetService(context).Resolve(context.Context(), party)
			if err != nil {
				return nil, errors.Wrapf(err, "cannot resolve long term auditor identity for [%s]", party.UniqueID())
			}
		}
		distributionListCompressed = append(distributionListCompressed, distributionListEntry{
			IsMe:     isMe,
			ID:       party,
			Auditor:  true,
			LongTerm: longTermIdentity,
		})
	}

	logger.DebugfContext(context.Context(), "distributed tx to num parties [%d]", len(distributionListCompressed))

	return distributionListCompressed, nil
}

// getSession retrieves an existing session for the given party from the cache,
// or creates a new session if one doesn't exist.
func (c *CollectEndorsementsView) getSession(context view.Context, p view.Identity) (view.Session, error) {
	s, ok := c.sessions[p.UniqueID()]
	if ok {
		logger.DebugfContext(context.Context(), "getSession: found session for [%s]", p.UniqueID())

		return s, nil
	}

	return context.GetSession(context.Initiator(), p)
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
