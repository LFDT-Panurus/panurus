/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// errNotImplemented marks the surface that lands with the finality manager.
var errNotImplemented = errors.New("evm: not implemented")

// EndorsementService is the endorsement entry point the driver drives to collect a quorum. It is
// satisfied by *endorsement.Service and narrowed here so the network can be tested with a stub.
type EndorsementService interface {
	// Endorse collects a quorum of endorsements for the request and returns the assembled result.
	Endorse(context view.Context, req *endorsement.EndorseRequest) (*endorsement.Result, error)
}

// Network is the EVM implementation of driver.Network for one network. It owns the pieces a token
// transaction travels through: the endorsement service that authorizes a delta, the submitter that
// puts it on chain, and the contract reader that answers queries.
type Network struct {
	name        string
	config      *Config
	client      client.EVMClient
	endorsement EndorsementService
	submitter   *Submitter
	reader      *contractReader
	tokenState  client.Address
}

// Compile-time assertion that Network satisfies the driver contract.
var _ driver.Network = (*Network)(nil)

// NewNetwork assembles an EVM Network from its already-resolved collaborators.
func NewNetwork(
	name string,
	config *Config,
	evmClient client.EVMClient,
	endorsementService EndorsementService,
	submitter *Submitter,
) (*Network, error) {
	if config == nil {
		return nil, errors.New("evm network: nil configuration")
	}
	if evmClient == nil {
		return nil, errors.New("evm network: nil evm client")
	}
	tokenState, err := config.TokenStateAddress()
	if err != nil {
		return nil, err
	}

	return &Network{
		name:        name,
		config:      config,
		client:      evmClient,
		endorsement: endorsementService,
		submitter:   submitter,
		reader:      newContractReader(evmClient, tokenState, config.Finality.BlockTag),
		tokenState:  tokenState,
	}, nil
}

// Name returns the identifier of the network.
func (n *Network) Name() string { return n.name }

// Channel returns the empty string: EVM has no channel concept.
func (n *Network) Channel() string { return "" }

// Normalize fills default service options for the EVM network: this network and the empty channel.
func (n *Network) Normalize(opt *token2.ServiceOptions) (*token2.ServiceOptions, error) {
	if opt == nil {
		return nil, errors.New("evm network: nil service options")
	}
	if len(opt.Network) == 0 {
		opt.Network = n.name
	}
	if len(opt.Channel) != 0 && opt.Channel != n.Channel() {
		return nil, errors.Errorf("evm network has no channels, got [%s]", opt.Channel)
	}
	opt.Channel = n.Channel()

	return opt, nil
}

// Connect validates that the configured backend is reachable and is the chain the driver signs for,
// then returns the service options that bind a TMS to this network. Checking here means a
// misconfigured endpoint or chain id fails at startup rather than at the first transaction.
func (n *Network) Connect(ns string) ([]token2.ServiceOption, error) {
	ctx := context.Background()
	chainID, err := n.client.ChainID(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "evm network: node at [%s] is not reachable", n.config.Endpoint)
	}
	if chainID.Cmp(n.config.ChainIDBig()) != 0 {
		return nil, errors.Errorf("evm network: node reports chain id %s, configuration says %d",
			chainID, n.config.ChainID)
	}

	return []token2.ServiceOption{
		token2.WithNetwork(n.name),
		token2.WithChannel(n.Channel()),
		token2.WithNamespace(ns),
	}, nil
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
	if n.endorsement == nil {
		return nil, errors.New("evm network: no endorsement service configured")
	}
	if tms == nil {
		return nil, errors.New("evm network: nil token management service")
	}

	anchor := n.ComputeTxID(&txID)
	result, err := n.endorsement.Endorse(context, &endorsement.EndorseRequest{
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
		Delta:        result.Delta,
		Endorsements: result.Endorsements,
	}, nil
}

// Broadcast assembles the endorsed envelope into a signed transaction, sends it, and records the
// resulting raw transaction and hash back into the envelope so the caller can follow its finality.
func (n *Network) Broadcast(ctx context.Context, blob any) error {
	env, ok := blob.(*Envelope)
	if !ok {
		return errors.Errorf("evm network: expected *Envelope, got [%T]", blob)
	}
	if n.submitter == nil {
		return errors.New("evm network: no submitter configured")
	}
	if env.Delta == nil {
		return errors.Errorf("evm network: envelope [%s] carries no state delta", env.Anchor)
	}

	rawTx, txHash, err := n.submitter.Submit(ctx, env.Delta, env.Endorsements)
	if err != nil {
		return errors.Wrapf(err, "failed to broadcast [%s]", env.Anchor)
	}
	env.RawTx = rawTx
	env.EthTxHash = txHash.Hex()

	return nil
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
	if n.endorsement == nil {
		return nil, errors.New("evm network: no endorsement service configured")
	}

	anchor := n.ComputeTxID(&txID)
	result, err := n.endorsement.Endorse(context, &endorsement.EndorseRequest{
		TokenRequest: publicParamsRaw,
		TMSID:        tmsID,
		Anchor:       anchor,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to collect endorsements for public parameters [%s]", anchor)
	}

	return &Envelope{
		Anchor:       anchor,
		Delta:        result.Delta,
		Endorsements: result.Endorsements,
	}, nil
}

// FetchPublicParameters retrieves the public parameters currently stored in the TokenState contract.
func (n *Network) FetchPublicParameters(namespace string) ([]byte, error) {
	return n.reader.publicParameters(context.Background())
}

// QueryTokens reads the stored bytes of the given tokens. A token that does not exist on chain is an
// error rather than an empty entry, because the caller asked for tokens it believes exist and a
// silent gap would read as a valid empty token.
func (n *Network) QueryTokens(ctx context.Context, namespace string, ids []*token.ID) ([][]byte, error) {
	out := make([][]byte, len(ids))
	for i, id := range ids {
		data, err := n.reader.tokenData(ctx, id)
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

// AreTokensSpent checks the spent status of the given tokens, aligned with the input. It resolves
// through the content-bound marker recorded at creation, so only the ids are needed.
func (n *Network) AreTokensSpent(
	ctx context.Context,
	namespace string,
	tokenIDs []*token.ID,
	meta []string,
) ([]bool, error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}

	return n.reader.spent(ctx, tokenIDs)
}

// LookupTransferMetadataKey reads a transfer metadata value from the TokenState contract, polling
// until it appears or the timeout expires. The value is written when the transaction carrying it is
// applied, so a caller may legitimately ask before it exists.
func (n *Network) LookupTransferMetadataKey(namespace string, key string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	metadataKey := keys.TransferMetadataKey(key)
	ticker := time.NewTicker(n.config.Finality.PollInterval)
	defer ticker.Stop()

	for {
		value, err := n.reader.transferMetadata(ctx, metadataKey)
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

// LocalMembership returns the local membership service for EVM identities. It lands with the driver
// wiring.
func (n *Network) LocalMembership() driver.LocalMembership { return nil }

// AddFinalityListener registers a listener for the finality of a transaction. It lands with the
// finality manager.
func (n *Network) AddFinalityListener(namespace string, txID string, listener driver.FinalityListener) error {
	return errNotImplemented
}

// GetTransactionStatus returns the validation status and token-request hash of a transaction. It
// lands with the finality manager.
func (n *Network) GetTransactionStatus(ctx context.Context, namespace, txID string) (int, []byte, string, error) {
	return driver.Unknown, nil, "", errNotImplemented
}

// Ledger returns the read-only EVM ledger adapter. It lands with the finality manager, whose status
// resolution it shares.
func (n *Network) Ledger() (driver.Ledger, error) { return nil, errNotImplemented }
