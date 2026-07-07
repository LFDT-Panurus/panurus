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
)

// errNotImplemented is returned by the Network methods that are not wired up yet. The skeleton
// exists so registration and routing can be validated before the behaviour lands (Phases 3-6).
var errNotImplemented = errors.New("evm: not implemented")

// Network is the EVM implementation of driver.Network. In Phase 1.2 every behavioural method is a
// stub returning errNotImplemented; the real implementations are filled in from Phase 3 onwards.
type Network struct {
	name string
}

// Compile-time assertion that Network satisfies the driver contract.
var _ driver.Network = (*Network)(nil)

func newNetwork(name string) *Network {
	return &Network{name: name}
}

// Name returns the identifier of the network.
func (n *Network) Name() string { return n.name }

// Channel returns the empty string: EVM has no channel concept.
func (n *Network) Channel() string { return "" }

// Normalize fills default service options for the EVM network.
func (n *Network) Normalize(opt *token2.ServiceOptions) (*token2.ServiceOptions, error) {
	return nil, errNotImplemented
}

// Connect initializes namespace-scoped services for the EVM network.
func (n *Network) Connect(ns string) ([]token2.ServiceOption, error) {
	return nil, errNotImplemented
}

// Broadcast submits a signed transaction envelope to the EVM node.
func (n *Network) Broadcast(ctx context.Context, blob any) error {
	return errNotImplemented
}

// NewEnvelope returns a new, empty EVM envelope.
func (n *Network) NewEnvelope() driver.Envelope { return &Envelope{} }

// RequestApproval collects endorsements and assembles the EVM transaction envelope.
func (n *Network) RequestApproval(
	context view.Context,
	tms *token2.ManagementService,
	requestRaw []byte,
	signer view.Identity,
	txID driver.TxID,
	metadata driver.TransientMap,
) (driver.Envelope, error) {
	return nil, errNotImplemented
}

// ComputeTxID computes the deterministic token-request anchor for the transaction.
func (n *Network) ComputeTxID(id *driver.TxID) string { return "" }

// FetchPublicParameters retrieves the public parameters from the TokenState contract.
func (n *Network) FetchPublicParameters(namespace string) ([]byte, error) {
	return nil, errNotImplemented
}

// QueryTokens reads token data for the given IDs from the TokenState contract.
func (n *Network) QueryTokens(ctx context.Context, namespace string, ids []*token.ID) ([][]byte, error) {
	return nil, errNotImplemented
}

// AreTokensSpent checks the spent status of the given tokens on-chain.
func (n *Network) AreTokensSpent(ctx context.Context, namespace string, tokenIDs []*token.ID, meta []string) ([]bool, error) {
	return nil, errNotImplemented
}

// LocalMembership returns the local membership service for EVM identities.
func (n *Network) LocalMembership() driver.LocalMembership { return nil }

// AddFinalityListener registers a listener for the finality of a transaction.
func (n *Network) AddFinalityListener(namespace string, txID string, listener driver.FinalityListener) error {
	return errNotImplemented
}

// GetTransactionStatus returns the validation status and token-request hash of a transaction.
func (n *Network) GetTransactionStatus(ctx context.Context, namespace, txID string) (int, []byte, string, error) {
	return 0, nil, "", errNotImplemented
}

// LookupTransferMetadataKey reads transfer metadata from the TokenState contract.
func (n *Network) LookupTransferMetadataKey(namespace string, key string, timeout time.Duration) ([]byte, error) {
	return nil, errNotImplemented
}

// Ledger returns the read-only EVM ledger adapter.
func (n *Network) Ledger() (driver.Ledger, error) { return nil, errNotImplemented }
