/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	mem "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

func NewInMemory() (KVS, error) {
	k, err := kvs.New(utils.MustGet(mem.NewDriver().NewKVS("")), "", kvs.DefaultCacheSize)
	if err != nil {
		return nil, err
	}

	return &fscKVS{KVS: k}, nil
}

func Keystore(kvs KVS) *kvsAdapter {
	return &kvsAdapter{kvs: kvs}
}

type kvsAdapter struct {
	kvs KVS
}

func (k *kvsAdapter) Put(id string, state any) error {
	return k.kvs.Put(context.Background(), id, state)
}

func (k *kvsAdapter) Get(id string, state any) error {
	return k.kvs.Get(context.Background(), id, state)
}

func (k *kvsAdapter) Close() error {
	return k.kvs.Close()
}

func (k *kvsAdapter) Delete(id string) error {
	return k.kvs.Delete(context.Background(), id)
}

type fscKVS struct {
	*kvs.KVS
}

func (k *fscKVS) Close() error {
	k.Stop()

	return nil
}

// notFoundMarker is the substring FSC's KVS.Get uses to report that a key holds
// no value (kvs.KVS.Get -> "state [ns,id] does not exist"). It is the only signal
// available to tell "confirmed absent" from a real backing-store failure, since
// both are returned as untyped errors.
const notFoundMarker = "does not exist"

// GetExisting returns the subset of ids that currently hold a value.
//
// The embedded FSC kvs.KVS.GetExisting swallows a backing-store failure and
// returns a partial (or empty) list with no error, which lets a transient store
// outage after a cold-cache restart be mistaken for "not mine" (see
// Provider.areMe / #2066). This override probes each id with the error-returning
// kvs.KVS.Get instead: a nil error means present, a "does not exist" error means
// confirmed absent, and any other error is propagated so callers do not treat an
// unchecked id as absent.
func (k *fscKVS) GetExisting(ctx context.Context, ids ...string) ([]string, error) {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		var raw json.RawMessage
		// k.Get is the embedded kvs.KVS.Get (fscKVS defines no Get of its own).
		err := k.Get(ctx, id, &raw)
		switch {
		case err == nil:
			result = append(result, id)
		case strings.Contains(err.Error(), notFoundMarker):
			// confirmed absent, skip
		default:
			return nil, errors.Wrapf(err, "failed checking existence of [%s]", id)
		}
	}

	return result, nil
}
