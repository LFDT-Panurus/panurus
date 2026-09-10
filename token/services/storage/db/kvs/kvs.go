/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

type KVS interface {
	Exists(ctx context.Context, id string) bool
	// GetExisting returns the subset of ids that currently hold a value. A non-nil
	// error means existence could not be determined against the backing store; the
	// returned slice is then not authoritative and must not be read as "only these
	// exist" (see Provider.areMe / #2066). Implementations must not fold a store
	// failure into a shorter slice with a nil error.
	GetExisting(ctx context.Context, ids ...string) ([]string, error)
	Put(ctx context.Context, id string, state any) error
	Get(ctx context.Context, id string, state any) error
	GetByPartialCompositeID(ctx context.Context, prefix string, attrs []string) (kvs.Iterator, error)
	Close() error
	Delete(ctx context.Context, id string) error
}
