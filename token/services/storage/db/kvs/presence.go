/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"encoding/json"
	"strings"
)

// notFoundSuffix and notFoundHeads match the absence errors of the FSC in-tree KVS
// ("state [ns,id] does not exist") and of older Vault KVS releases ("state of id [id] does not
// exist"); the Vault module is versioned independently, so keep that head. The leading space
// anchors the suffix on a word boundary.
const notFoundSuffix = " does not exist"

var notFoundHeads = []string{"state [", "state of id ["}

// isNotFoundErr reports whether err is a KVS "key not found" error rather than a store failure.
// FSC has no absence sentinel yet, so the match is on the error message, anchored at both ends:
// store failures are wrapped and so never match, even when their cause says "does not exist"
// (e.g. Postgres `relation "kvs" does not exist`). Treating that as a miss would make
// GetWalletID answer ("", nil) and the role Registry create a duplicate wallet (#2063); a too
// strict match only errors loudly. Never loosen it; replace it with a sentinel.
// TestIsNotFoundErrMatchesLiveBackend catches upstream rewording.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	if !strings.HasSuffix(msg, notFoundSuffix) {
		return false
	}

	for _, head := range notFoundHeads {
		if strings.HasPrefix(msg, head) {
			return true
		}
	}

	return false
}

// getRaw reads the raw JSON stored under k. found=false with a nil error means an authoritative
// miss: a not-found error (FSC in-tree KVS) or an untouched destination (Vault KVS). Any other
// error is returned unchanged, so it is never mistaken for absence. A stored JSON null is found.
// TODO(#2063): use the FSC sentinel once it lands.
func getRaw(ctx context.Context, s KVS, k string) (json.RawMessage, bool, error) {
	var raw json.RawMessage
	if err := s.Get(ctx, k, &raw); err != nil {
		if isNotFoundErr(err) {
			return nil, false, nil
		}

		return nil, false, err
	}
	if raw == nil {
		return nil, false, nil
	}

	return raw, true, nil
}
