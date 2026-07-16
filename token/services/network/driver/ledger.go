/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
)

// Ledger models the ledger service
type Ledger interface {
	// Status returns the validation code of the transaction with the given ID.
	// If the transaction cannot be retrieved from the ledger, it returns Unknown
	// together with a non-nil error. Implementations are not required to agree on
	// whether a pending (not-yet-committed) transaction is reported as Unknown or
	// Invalid; callers should not rely on Unknown being returned without an error.
	Status(id string) (ValidationCode, error)
	// GetTransactionStatus retrieves the current status and token request hash for a transaction.
	// It returns the validation status, the token request hash stored for namespace (if any),
	// a human-readable status message, and any error encountered while contacting the ledger.
	// The token request hash is populated only when status is Valid; for any other status it is
	// nil. A Valid transaction with no token request hash recorded for namespace also yields a
	// nil hash, not an error.
	GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error)
	// GetStates returns the raw value stored at each of the given keys in the given namespace.
	// The returned slice has exactly one entry per requested key, in the same order; a key with
	// no corresponding state on the ledger yields a nil entry at that position rather than an
	// error. Implementations may disagree on the zero-keys case: some return (nil, nil), others
	// return an error, so callers should avoid invoking GetStates with no keys.
	GetStates(ctx context.Context, namespace string, keys ...string) ([][]byte, error)
	// TransferMetadataKey returns the ledger key associated to the given transfer metadata sub-key.
	// It is a pure key-derivation function backed by the network's key translator and does not
	// touch the ledger, so it does not indicate whether the resulting key actually exists.
	TransferMetadataKey(k string) (string, error)
}
