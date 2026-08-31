/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	ncommon "github.com/LFDT-Panurus/panurus/token/services/network/common"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/finality"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// EndorsementService is the endorsement entry point the driver drives to collect a quorum. It is
// satisfied by *endorsement.Service and narrowed here so the network can be tested with a stub.
type EndorsementService interface {
	// Endorse collects a quorum of endorsements for the request and returns the assembled result.
	Endorse(context view.Context, req *endorsement.EndorseRequest) (*endorsement.Result, error)
}

// namespaceBinding is the per-TMS state a shared EVM Network resolves once at construction: the
// reader, submitter and finality manager bound to that one TMS's own configured TokenState clone.
//
// It exists because token/services/network.Provider memoizes one *Network per (network, channel),
// with the namespace not part of that key (see that package's netId/key), so every TMS declaring the
// same EVM network shares one *Network object - and EVM has no channel to separate them by either
// (Channel always returns ""). Without per-namespace state, whichever TMS's configuration Driver.New
// happened to resolve first would silently back every other TMS's reads, writes and endorsement
// domain too. Building one binding per TMS, keyed by namespace, is what keeps a second TMS on the
// same network reading and writing through its own contract instead of the first one's.
type namespaceBinding struct {
	config     *Config
	submitter  *Submitter
	reader     *contractReader
	finality   *finality.Manager
	tokenState client.Address
}

// Network is the EVM implementation of driver.Network for one network. It owns the pieces a token
// transaction travels through: the endorsement service that authorizes a delta, the submitter that
// puts it on chain, and the contract reader that answers queries - one of each per TMS sharing this
// network, see namespaceBinding.
type Network struct {
	name        string
	client      client.EVMClient
	endorsement EndorsementService
	// endorsementFor resolves the per-TMS endorsement service. It is preferred over the field above,
	// which stays for tests that inject a stub directly.
	endorsementFor func(tms *token2.ManagementService) (EndorsementService, error)
	// endorsementForID is endorsementFor's counterpart for callers that only have a TMSID, not an
	// already-built management service: SetupPublicParams needs this, since first-time setup of a
	// namespace runs before a management service can exist for it to have one at all.
	endorsementForID func(tmsID token2.TMSID) (EndorsementService, error)
	membership       driver.LocalMembership
	// startRecovery starts the per-TMS transaction recovery sweep. It is installed by the driver,
	// which owns the stores, and runs when a namespace binds rather than when the network is built.
	startRecovery func(ns string) error
	// bindings holds every TMS's own reader/submitter/finality manager, keyed by namespace. It is
	// built once, from every TMS the driver told NewNetwork about, and never mutated afterward - so
	// reading it needs no lock even though Connect/QueryTokens/Broadcast run concurrently.
	bindings map[string]*namespaceBinding
}

// Compile-time assertion that Network satisfies the driver contract.
var _ driver.Network = (*Network)(nil)

// NamespaceConfig pairs one TMS's namespace with its own resolved configuration and submitter, so
// NewNetwork can build that TMS's own namespaceBinding instead of one shared instance reused by every
// TMS on the network.
type NamespaceConfig struct {
	// Namespace is the TMS's namespace within this network.
	Namespace string
	// Config is this TMS's own EVM configuration, resolved from its own services.network.evm block -
	// not necessarily the same TokenState, endorsement policy, submitter or gas policy as any other
	// TMS sharing this network.
	Config *Config
	// Submitter is this TMS's own submitter, built from Config.Submitter and bound to Config's
	// TokenState. Nil when the TMS has no submitter key configured, the same "reads only" case
	// Driver.newSubmitter already allows.
	Submitter *Submitter
}

// NewNetwork assembles an EVM Network from its already-resolved collaborators. namespaces must
// contain every TMS this network will ever be asked to serve: Connect, QueryTokens, Broadcast and
// every other per-namespace operation only ever look up a namespace that was passed in here, they
// never build one lazily (design: a bad configuration is a startup error, never a surprise at the
// first transaction).
func NewNetwork(
	name string,
	evmClient client.EVMClient,
	namespaces []NamespaceConfig,
	endorsementService EndorsementService,
	membership driver.LocalMembership,
) (*Network, error) {
	if evmClient == nil {
		return nil, errors.New("evm network: nil evm client")
	}
	if len(namespaces) == 0 {
		return nil, errors.New("evm network: no tms configured for this network")
	}

	bindings := make(map[string]*namespaceBinding, len(namespaces))
	for _, nc := range namespaces {
		if nc.Namespace == "" {
			return nil, errors.New("evm network: namespace configuration without a namespace")
		}
		if nc.Config == nil {
			return nil, errors.Errorf("evm network: nil configuration for namespace [%s]", nc.Namespace)
		}
		tokenState, err := nc.Config.TokenStateAddress()
		if err != nil {
			return nil, err
		}

		reader := newContractReader(evmClient, tokenState, nc.Config.Finality.BlockTag)
		bindings[nc.Namespace] = &namespaceBinding{
			config:     nc.Config,
			submitter:  nc.Submitter,
			reader:     reader,
			tokenState: tokenState,
			finality: finality.NewManager(
				evmClient, reader, tokenState, nc.Config.Finality.FromBlock, nc.Config.Finality.BlockTag,
				nc.Config.Finality.PollInterval, nc.Config.Finality.Timeout,
			),
		}
	}

	return &Network{
		name:        name,
		client:      evmClient,
		endorsement: endorsementService,
		membership:  membership,
		bindings:    bindings,
	}, nil
}

// binding returns the namespace's own resolved state, or an error naming it when this network was
// never told about it - the same "fail loud, not silently wrong" contract Connect already applies to
// a bad endpoint or chain id.
func (n *Network) binding(ns string) (*namespaceBinding, error) {
	b, ok := n.bindings[ns]
	if !ok {
		return nil, errors.Errorf(
			"evm network: no configuration bound for namespace [%s]; check its services.network.evm block",
			ns)
	}

	return b, nil
}

// Name returns the identifier of the network.
func (n *Network) Name() string { return n.name }

// Channel returns the empty string: EVM has no channel concept.
func (n *Network) Channel() string { return "" }

// Normalize fills default service options for the EVM network: this network, the empty channel, and
// the fetcher the token layer reads public parameters through.
//
// The fetcher matters more than it looks. Building a TMS needs its public parameters, and the token
// layer tries the options, its storage, the local configuration and finally this fetcher. On a fresh
// node none of the first three hold anything, so without a fetcher here the parameters cannot be
// found at all and the TMS fails to build, which surfaces far from its cause.
func (n *Network) Normalize(opt *token2.ServiceOptions) (*token2.ServiceOptions, error) {
	if opt == nil {
		return nil, errors.New("evm network: nil service options")
	}
	if len(opt.Network) == 0 {
		opt.Network = n.name
	}
	if opt.Network != n.name {
		return nil, errors.Errorf("evm network: invalid network [%s], expected [%s]", opt.Network, n.name)
	}
	if len(opt.Channel) != 0 && opt.Channel != n.Channel() {
		return nil, errors.Errorf("evm network has no channels, got [%s]", opt.Channel)
	}
	opt.Channel = n.Channel()

	if opt.PublicParamsFetcher == nil {
		opt.PublicParamsFetcher = ncommon.NewPublicParamsFetcher(n, opt.Namespace)
	}

	return opt, nil
}

// Connect validates that the configured backend is reachable, is the chain the driver signs for, and
// enforces the endorsement policy this node is configured with, then returns the service options that
// bind a TMS to this network. Checking here means a misconfigured endpoint, chain id or endorser set
// fails at startup rather than at the first transaction.
func (n *Network) Connect(ns string) ([]token2.ServiceOption, error) {
	binding, err := n.binding(ns)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	chainID, err := n.client.ChainID(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "evm network: node at [%s] is not reachable", binding.config.Endpoint)
	}
	if chainID.Cmp(binding.config.ChainIDBig()) != 0 {
		return nil, errors.Errorf("evm network: node reports chain id %s, configuration says %d",
			chainID, binding.config.ChainID)
	}
	if err := n.verifyTokenStateDeployed(ctx, binding); err != nil {
		return nil, err
	}
	if err := n.verifyEndorsementPolicy(ctx, binding); err != nil {
		return nil, err
	}

	// Recovery completes transactions this node left Pending when it last stopped. Failing to start
	// it is not a reason to refuse the connection: the node works, it just cannot rescue those.
	if n.startRecovery != nil {
		if err := n.startRecovery(ns); err != nil {
			logger.Errorf("could not start transaction recovery for [%s]: %v", ns, err)
		}
	}

	return []token2.ServiceOption{
		token2.WithNetwork(n.name),
		token2.WithChannel(n.Channel()),
		token2.WithNamespace(ns),
	}, nil
}

// verifyTokenStateDeployed refuses to connect when contracts.tokenState holds no contract.
//
// This is the most ordinary way an EVM deployment is misconfigured: a typo, a configuration copied
// between environments, or a node pointed at a chain where the deploy has not happened. None of it is
// visible without asking, because eth_call against an address with no code is not an error on any
// node, it returns empty data. Every read the driver makes therefore comes back empty rather than
// failing, the startup checks that read through eth_call find nothing to contradict, and the node
// comes up looking healthy. The cost is paid one transaction at a time, as failures that do not look
// like configuration problems.
//
// A failed read is not treated as a missing contract. It says the node could not be asked, which is
// the same reason the policy check below tolerates one, and taking a node down over a momentarily
// unhappy endpoint would be worse than the problem being guarded against.
func (n *Network) verifyTokenStateDeployed(ctx context.Context, binding *namespaceBinding) error {
	code, err := n.client.CodeAt(ctx, binding.tokenState, client.BlockTagLatest)
	if err != nil {
		logger.Warnf("could not read the code at tokenState [%s]; skipping the deployment check: %v",
			binding.tokenState, err)

		return nil
	}
	if len(code) == 0 {
		return errors.Errorf(
			"evm network: no contract is deployed at tokenState [%s] on chain %d; "+
				"check contracts.tokenState against the chain this endpoint serves",
			binding.tokenState, binding.config.ChainID)
	}

	return nil
}

// NewEnvelope returns a new, empty EVM envelope.
func (n *Network) NewEnvelope() driver.Envelope { return &Envelope{} }

// RequestApproval collects a quorum of endorsements over the token request and returns the envelope
// carrying the endorsed delta. It does not touch the chain: the transaction is assembled and sent by
// Broadcast, so a request that fails to gather a quorum costs no gas.
func (n *Network) RequestApproval(
	context view.Context,
	tms *token2.ManagementService,
	requestRaw []byte,
	signer view.Identity,
	txID driver.TxID,
	metadata driver.TransientMap,
) (driver.Envelope, error) {
	if tms == nil {
		return nil, errors.New("evm network: nil token management service")
	}
	endorser, err := n.endorserFor(tms)
	if err != nil {
		return nil, err
	}

	anchor := n.ComputeTxID(&txID)
	result, err := endorser.Endorse(context, &endorsement.EndorseRequest{
		TokenRequest: requestRaw,
		TMSID:        tms.ID(),
		Anchor:       anchor,
		Metadata:     metadata,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to collect endorsements for [%s]", anchor)
	}

	return &Envelope{
		Anchor:       anchor,
		Namespace:    tms.ID().Namespace,
		Delta:        result.Delta,
		Endorsements: result.Endorsements,
	}, nil
}

// endorserFor returns the endorsement service for a TMS: the one injected directly if there is one,
// otherwise the per-TMS service the factory builds. The service is per TMS because validating a
// request needs that TMS's validator.
func (n *Network) endorserFor(tms *token2.ManagementService) (EndorsementService, error) {
	if n.endorsement != nil {
		return n.endorsement, nil
	}
	if n.endorsementFor == nil {
		return nil, errors.New("evm network: no endorsement service configured")
	}

	return n.endorsementFor(tms)
}

// SetEndorsementFactory installs the resolver that builds the endorsement service per TMS. The SDK
// wiring calls it after the network is created, because the factory needs collaborators (the view
// manager, this node's signing key) that the network itself has no source for.
func (n *Network) SetEndorsementFactory(f func(tms *token2.ManagementService) (EndorsementService, error)) {
	n.endorsementFor = f
}

// endorserForID is endorserFor's counterpart for a bare TMS id: the one injected directly if there is
// one, otherwise the per-TMS service the factory builds. SetupPublicParams uses this rather than
// endorserFor because it may run before a management service exists for the TMS at all.
func (n *Network) endorserForID(tmsID token2.TMSID) (EndorsementService, error) {
	if n.endorsement != nil {
		return n.endorsement, nil
	}
	if n.endorsementForID == nil {
		return nil, errors.New("evm network: no endorsement service configured")
	}

	return n.endorsementForID(tmsID)
}

// SetEndorsementFactoryByID installs the resolver endorserForID uses. It is SetEndorsementFactory's
// counterpart for the id-only path.
func (n *Network) SetEndorsementFactoryByID(f func(tmsID token2.TMSID) (EndorsementService, error)) {
	n.endorsementForID = f
}

// SetRecoveryStarter installs the hook that starts transaction recovery for a namespace. Like the
// endorsement factory it is set by the driver after construction, because the stores it sweeps
// belong to the driver rather than to the network.
func (n *Network) SetRecoveryStarter(f func(ns string) error) { n.startRecovery = f }

// Broadcast assembles the endorsed envelope into a signed transaction, sends it, and records the
// resulting raw transaction and hash back into the envelope so the caller can follow its finality.
//
// It checks that the envelope's anchor and its delta's own anchor agree before spending any gas.
// Under the normal flow they always do, since both are derived from the same anchor at the point an
// Envelope is built (RequestApproval, SetupPublicParams), but Broadcast has no way to know how the
// envelope it was actually handed got here, and a mismatch here is not hypothetical: it would mean
// the local bookkeeping that tracks this transaction (finality listeners, the ttx store) is keyed on
// one anchor while the chain applies and emits StateCommitted for a different one. A finality wait on
// the anchor Broadcast was told about would then watch an anchor the chain never touches, and after
// the timeout wrongly report a transaction that actually succeeded as failed, the same failure shape
// as a persistent read error, just from a construction bug instead of a chain-connectivity one.
func (n *Network) Broadcast(ctx context.Context, blob any) error {
	env, ok := blob.(*Envelope)
	if !ok {
		return errors.Errorf("evm network: expected *Envelope, got [%T]", blob)
	}
	binding, err := n.binding(env.Namespace)
	if err != nil {
		return err
	}
	if binding.submitter == nil {
		return errors.New("evm network: no submitter configured")
	}
	if env.Delta == nil {
		return errors.Errorf("evm network: envelope [%s] carries no state delta", env.Anchor)
	}
	anchor, err := keys.AnchorFromTxID(env.Anchor)
	if err != nil {
		return errors.Wrapf(err, "evm network: envelope carries an invalid anchor [%s]", env.Anchor)
	}
	if env.Delta.Anchor != anchor {
		return errors.Errorf(
			"evm network: envelope anchor [%s] does not match its delta's anchor [%x]; refusing to broadcast",
			env.Anchor, env.Delta.Anchor)
	}

	rawTx, txHash, err := binding.submitter.Submit(ctx, env.Delta, env.Endorsements)
	if err != nil {
		// A permanent rejection is not the whole story while the anchor is already on chain.
		//
		// The ordinary way to arrive here is a retry after a broadcast whose reply went missing: the
		// first attempt was mined, the caller never learned that, and the contract now refuses the
		// anchor for exactly the right reason, that applying it twice would be a double spend. The
		// rejection is about this second attempt. The transaction the caller is asking about
		// succeeded, so answering "invalid" would have it discard a transfer that is final.
		//
		// Only a permanent rejection is worth this second look. A transient failure leaves the caller
		// retrying anyway, which is the correct outcome whether or not the anchor ever landed.
		if errors.Is(err, ErrTransactionReverted) && n.anchorApplied(ctx, binding, anchor) {
			logger.Infof(
				"the apply for [%s] was refused because the anchor is already on chain; "+
					"treating the transaction as the success it is", env.Anchor)

			return nil
		}

		return errors.Wrapf(err, "failed to broadcast [%s]", env.Anchor)
	}
	env.RawTx = rawTx
	env.EthTxHash = txHash.Hex()

	return nil
}

// anchorApplied reports whether the chain has already recorded this anchor.
//
// It answers false when the status cannot be read, which keeps the original rejection: a node that
// cannot be asked is no reason to convert a refusal into a success, and the caller is better served
// by the error it would have had anyway.
//
// The envelope keeps no transaction hash in this case, because the hash belongs to the attempt that
// landed and this process never saw it. Nothing downstream needs it: finality is waited on by anchor,
// and that anchor resolves immediately.
func (n *Network) anchorApplied(ctx context.Context, binding *namespaceBinding, anchor [32]byte) bool {
	if binding.finality == nil {
		return false
	}
	code, _, _, err := binding.finality.StatusByAnchor(ctx, anchor)
	if err != nil {
		logger.Debugf("could not check whether [%x] is already applied: %v", anchor, err)

		return false
	}

	return code == driver.Valid
}

// ComputeTxID returns the token-request anchor for the transaction.
//
// It MUTATES id: when the nonce is empty it generates a fresh random one and writes it back, which is
// the contract FSC and the Fabric driver implement and the ttx layer relies on. A read-only
// implementation would derive the same anchor for every transaction of a creator, and the second one
// submitted would revert as an already-processed anchor (design §5.3).
func (n *Network) ComputeTxID(id *driver.TxID) string {
	if id == nil {
		return ""
	}
	if len(id.Nonce) == 0 {
		nonce, err := generateNonce()
		if err != nil {
			// Matching FSC, which panics here: without a nonce there is no safe anchor to return, and
			// a randomness failure is not something a caller can act on.
			panic(err)
		}
		id.Nonce = nonce
	}

	return computeAnchor(id.Nonce, id.Creator)
}

// SetupPublicParams collects endorsements for a public-parameters update. The endorsers translate the
// setup request into a setup delta, so the flow matches RequestApproval; it exists separately because
// it takes a TMSID rather than a management service, which is what makes first-time setup of a
// namespace with no public parameters possible.
func (n *Network) SetupPublicParams(
	context view.Context,
	tmsID token2.TMSID,
	publicParamsRaw []byte,
	signer view.Identity,
	txID driver.TxID,
) (driver.Envelope, error) {
	endorser, err := n.endorserForID(tmsID)
	if err != nil {
		return nil, err
	}

	anchor := n.ComputeTxID(&txID)
	result, err := endorser.Endorse(context, &endorsement.EndorseRequest{
		TokenRequest: publicParamsRaw,
		TMSID:        tmsID,
		Anchor:       anchor,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to collect endorsements for public parameters [%s]", anchor)
	}

	return &Envelope{
		Anchor:       anchor,
		Namespace:    tmsID.Namespace,
		Delta:        result.Delta,
		Endorsements: result.Endorsements,
	}, nil
}

// FetchPublicParameters retrieves the public parameters currently stored in namespace's own
// TokenState contract.
func (n *Network) FetchPublicParameters(namespace string) ([]byte, error) {
	binding, err := n.binding(namespace)
	if err != nil {
		return nil, err
	}

	return binding.reader.publicParameters(context.Background())
}

// QueryTokens reads the stored bytes of the given tokens through namespace's own TokenState contract.
// A token that does not exist on chain is an error rather than an empty entry, because the caller
// asked for tokens it believes exist and a silent gap would read as a valid empty token.
func (n *Network) QueryTokens(ctx context.Context, namespace string, ids []*token.ID) ([][]byte, error) {
	binding, err := n.binding(namespace)
	if err != nil {
		return nil, err
	}

	out := make([][]byte, len(ids))
	for i, id := range ids {
		data, err := binding.reader.tokenData(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, errors.Errorf("token [%s:%d] does not exist", id.TxId, id.Index)
		}
		out[i] = data
	}

	return out, nil
}

// AreTokensSpent checks the spent status of the given tokens against namespace's own TokenState
// contract, aligned with the input. It resolves through the content-bound marker recorded at
// creation, so only the ids are needed.
func (n *Network) AreTokensSpent(
	ctx context.Context,
	namespace string,
	tokenIDs []*token.ID,
	meta []string,
) ([]bool, error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}
	binding, err := n.binding(namespace)
	if err != nil {
		return nil, err
	}

	return binding.reader.spent(ctx, tokenIDs)
}

// LookupTransferMetadataKey reads a transfer metadata value from namespace's own TokenState contract,
// polling until it appears or the timeout expires. The value is written when the transaction carrying
// it is applied, so a caller may legitimately ask before it exists.
func (n *Network) LookupTransferMetadataKey(namespace string, key string, timeout time.Duration) ([]byte, error) {
	binding, err := n.binding(namespace)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	metadataKey := keys.TransferMetadataKey(key)
	ticker := time.NewTicker(binding.config.Finality.PollInterval)
	defer ticker.Stop()

	for {
		value, err := binding.reader.transferMetadata(ctx, metadataKey)
		if err != nil {
			return nil, err
		}
		if len(value) != 0 {
			return value, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.Errorf("transfer metadata key [%s] not found within %s", key, timeout)
		case <-ticker.C:
		}
	}
}

// LocalMembership returns this node's identities. The driver does not mint identities of its own, so
// the service is supplied by the SDK wiring, which owns them; a network built without one reports nil
// rather than inventing a membership that would not correspond to any real node.
func (n *Network) LocalMembership() driver.LocalMembership { return n.membership }

// AddFinalityListener registers a listener for the finality of the transaction identified by its
// anchor. If the transaction is already final the listener is notified immediately; otherwise it is
// notified once, when the anchor appears on chain or the finality timeout expires.
func (n *Network) AddFinalityListener(namespace string, txID string, listener driver.FinalityListener) error {
	binding, err := n.binding(namespace)
	if err != nil {
		return err
	}
	anchor, err := keys.AnchorFromTxID(txID)
	if err != nil {
		return errors.Wrapf(err, "evm network: invalid transaction id [%s]", txID)
	}

	return binding.finality.AddListener(context.Background(), anchor, txID, listener)
}

// GetTransactionStatus returns the validation status and token-request hash of a transaction,
// identified by its anchor.
//
// Only success is observable by anchor: the contract records the anchor as the last step of a
// successful apply, while a failure reverts and leaves nothing behind. An unrecorded anchor is
// therefore Unknown, not Invalid; resolving it as failed is the finality timeout's job (design §7.4).
func (n *Network) GetTransactionStatus(ctx context.Context, namespace, txID string) (int, []byte, string, error) {
	binding, err := n.binding(namespace)
	if err != nil {
		return driver.Unknown, nil, "", err
	}
	anchor, err := keys.AnchorFromTxID(txID)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "evm network: invalid transaction id [%s]", txID)
	}

	return binding.finality.StatusByAnchor(ctx, anchor)
}

// Ledger returns the read-only ledger view, which answers the validation status of a transaction.
func (n *Network) Ledger() (driver.Ledger, error) { return &ledgerView{network: n}, nil }

// ledgerView adapts the network to the driver's Ledger contract.
type ledgerView struct {
	network *Network
}

// Status returns the validation code of the transaction with the given anchor.
//
// driver.Ledger.Status carries no namespace, unlike every other method on this interface, so it
// cannot say which TMS's TokenState to check once more than one shares this network. That is
// answerable without ambiguity only when this network serves exactly one TMS, which is what every
// caller in this repository (a per-TMS drift check) already builds against; a network serving more
// than one is asked to use GetTransactionStatus with an explicit namespace instead, rather than this
// method silently guessing which TMS's contract the caller meant.
func (l *ledgerView) Status(id string) (driver.ValidationCode, error) {
	if len(l.network.bindings) != 1 {
		return driver.Unknown, errors.Errorf(
			"evm network [%s]: Status has no namespace to disambiguate %d TMS sharing this network; "+
				"use GetTransactionStatus with an explicit namespace instead", l.network.name, len(l.network.bindings))
	}
	var namespace string
	for ns := range l.network.bindings {
		namespace = ns
	}

	code, _, _, err := l.network.GetTransactionStatus(context.Background(), namespace, id)
	if err != nil {
		return driver.Unknown, err
	}

	return code, nil
}

// GetTransactionStatus returns the status and token-request hash of a transaction by anchor.
func (l *ledgerView) GetTransactionStatus(
	ctx context.Context,
	namespace, txID string,
) (int, []byte, string, error) {
	return l.network.GetTransactionStatus(ctx, namespace, txID)
}

// GetStates returns the raw token bytes stored at each key, where a key is a token id in the
// hex-anchor:index form the driver addresses tokens by. A key with no state yields a nil entry.
func (l *ledgerView) GetStates(ctx context.Context, namespace string, keys ...string) ([][]byte, error) {
	binding, err := l.network.binding(namespace)
	if err != nil {
		return nil, err
	}

	out := make([][]byte, len(keys))
	for i, k := range keys {
		id, err := parseTokenKey(k)
		if err != nil {
			return nil, err
		}
		data, err := binding.reader.tokenData(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(data) != 0 {
			out[i] = data
		}
	}

	return out, nil
}

// TransferMetadataKey derives the on-chain key of a transfer metadata sub-key. It is pure derivation
// and says nothing about whether the key exists.
func (l *ledgerView) TransferMetadataKey(k string) (string, error) {
	key := keys.TransferMetadataKey(k)

	return hex.EncodeToString(key[:]), nil
}

// parseTokenKey parses a "<hex-anchor>:<index>" token key.
func parseTokenKey(k string) (*token.ID, error) {
	sep := strings.LastIndex(k, ":")
	if sep < 0 {
		return nil, errors.Errorf("evm ledger: malformed token key [%s], want <anchor>:<index>", k)
	}
	index, err := strconv.ParseUint(k[sep+1:], 10, 64)
	if err != nil {
		return nil, errors.Wrapf(err, "evm ledger: malformed token key [%s]", k)
	}

	return &token.ID{TxId: k[:sep], Index: index}, nil
}
