/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// FinalityListener defines the interface for receiving transaction status change notifications from the network.
type FinalityListener interface {
	// OnStatus is called when the validation status of a transaction changes on the ledger.
	OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte)
	// OnError is called when the finality event cannot be delivered after all retries are exhausted
	OnError(ctx context.Context, txID string, err error)
}

// TransientMap models the temporary, non-persisted metadata associated with a transaction proposal.
type TransientMap = map[string][]byte

// TxID represents a network-agnostic transaction identifier.
type TxID struct {
	// Nonce is a random value used to prevent replay attacks.
	Nonce []byte
	// Creator is the serialized identity of the entity that created the transaction.
	Creator []byte
}

// String returns a base64-encoded string representation of the transaction ID.
func (t *TxID) String() string {
	return fmt.Sprintf("[%s:%s]", base64.StdEncoding.EncodeToString(t.Nonce), base64.StdEncoding.EncodeToString(t.Creator))
}

// Network models a DLT backend that stores and validates token transactions.
type Network interface {
	// Name returns the identifier of the network.
	Name() string

	// Channel returns the name of the channel or ledger partition, if applicable.
	Channel() string

	// Normalize populates default values in service options based on network configuration.
	Normalize(opt *token2.ServiceOptions) (*token2.ServiceOptions, error)

	// Connect initializes the connection to the backend for a specific namespace.
	Connect(ns string) ([]token2.ServiceOption, error)

	// Broadcast submits a transaction or data blob to the network's ordering service.
	Broadcast(ctx context.Context, blob any) error

	// NewEnvelope creates a new, empty transaction envelope specific to the backend.
	NewEnvelope() Envelope

	// RequestApproval requests an endorsement for a token request from the network's approval service.
	// metadata carries optional application-level key-value pairs forwarded to the approver.
	RequestApproval(context view.Context, tms *token2.ManagementService, requestRaw []byte, signer view.Identity, txID TxID, metadata TransientMap) (Envelope, error)

	// ComputeTxID calculates the ledger-specific transaction ID from an abstract TxID.
	ComputeTxID(id *TxID) string

	// FetchPublicParameters retrieves the latest cryptographic public parameters from the ledger.
	FetchPublicParameters(namespace string) ([]byte, error)

	// QueryTokens retrieves raw token data for the specified IDs from the ledger state.
	QueryTokens(ctx context.Context, namespace string, IDs []*token.ID) ([][]byte, error)

	// AreTokensSpent checks the spent status of multiple tokens on the distributed ledger.
	AreTokensSpent(ctx context.Context, namespace string, tokenIDs []*token.ID, meta []string) ([]bool, error)

	// LocalMembership returns the local membership service for managing node identities.
	LocalMembership() LocalMembership

	// AddFinalityListener registers a listener for transaction status for the passed transaction id.
	// If the status is already valid or invalid, the listener is called immediately.
	// When the listener is invoked, then it is also removed.
	AddFinalityListener(namespace string, txID string, listener FinalityListener) error

	// GetTransactionStatus retrieves the current status and token request hash for a transaction.
	// Returns the validation status, token request hash, status message, and any error encountered.
	GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error)

	// LookupTransferMetadataKey scans the ledger for metadata associated with a transfer action.
	LookupTransferMetadataKey(namespace string, key string, timeout time.Duration) ([]byte, error)

	// Ledger provides access to the underlying ledger service for direct state interaction.
	Ledger() (Ledger, error)
}

// FinalityListenerManager defines the interface for managing transaction finality subscriptions.
type FinalityListenerManager interface {
	// AddFinalityListener registers a listener for transaction status for the passed transaction id.
	// If the status is already valid or invalid, the listener is called immediately.
	// When the listener is invoked, then it is also removed.
	AddFinalityListener(namespace string, txID string, listener FinalityListener) error
}
