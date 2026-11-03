/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

// KVS is the key-value store the identity and wallet stores of this package are built on. It is
// satisfied by the FSC in-tree KVS (see NewInMemory) and by the hashicorp vault KVS
// (token/services/storage/db/kvs/hashicorp).
//
// Error contract, which the stores in this package rely on:
//   - Get is the only point lookup that propagates a store error (GetByPartialCompositeID and
//     its iterator propagate store errors too). A missing id is reported either as an
//     error matching isNotFoundErr (FSC in-tree KVS) or as a nil error with state left untouched
//     (hashicorp vault KVS); any other error is a failed read and must not be read as absence.
//   - Exists and GetExisting are best-effort: they have no error return, so a failed read is
//     reported as "does not exist". The stores in this package therefore never use them to decide
//     presence; they read the entry with Get instead (see getRaw).
type KVS interface {
	// Exists reports whether a state is stored under id. A failed read is reported as false.
	Exists(ctx context.Context, id string) bool
	// GetExisting returns the subset of ids that hold a state. An id whose read fails is
	// reported as absent.
	GetExisting(ctx context.Context, ids ...string) []string
	// Put stores state, JSON-marshalled, under id, replacing whatever was stored there.
	Put(ctx context.Context, id string, state any) error
	// Get unmarshals the state stored under id into state. See the KVS error contract for how a
	// missing id is reported.
	Get(ctx context.Context, id string, state any) error
	// GetByPartialCompositeID returns an iterator over the states whose composite key starts with
	// the composite key built from prefix and attrs.
	GetByPartialCompositeID(ctx context.Context, prefix string, attrs []string) (kvs.Iterator, error)
	// Close releases the resources held by the KVS.
	Close() error
	// Delete removes the state stored under id.
	Delete(ctx context.Context, id string) error
}
