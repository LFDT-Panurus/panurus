/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

// KVS is the key-value store behind the identity and wallet stores: the FSC in-tree KVS or the
// hashicorp vault KVS.
//
// Error contract:
//   - Get propagates store errors. A missing id is either a not-found error (FSC in-tree KVS) or
//     a nil error with state untouched (Vault); any other error is a failed read, not absence.
//   - Exists and GetExisting have no error return, so a failed read looks like absence. They are
//     best-effort; use Get (see getRaw) to decide presence.
type KVS interface {
	// Exists reports whether a state is stored under id. A failed read yields false.
	Exists(ctx context.Context, id string) bool
	// GetExisting returns the subset of ids that hold a state. A failed read counts as absent.
	GetExisting(ctx context.Context, ids ...string) []string
	// Put stores state, JSON-marshalled, under id, replacing whatever was stored there.
	Put(ctx context.Context, id string, state any) error
	// Get unmarshals the state stored under id into state.
	Get(ctx context.Context, id string, state any) error
	// GetByPartialCompositeID returns an iterator over the states whose composite key starts with
	// the composite key built from prefix and attrs.
	GetByPartialCompositeID(ctx context.Context, prefix string, attrs []string) (kvs.Iterator, error)
	// Close releases the resources held by the KVS.
	Close() error
	// Delete removes the state stored under id.
	Delete(ctx context.Context, id string) error
}
