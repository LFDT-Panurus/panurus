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

// notFoundSuffix is the tail a KVS absence error ends in, and notFoundHeads the heads one
// begins with: the FSC in-tree KVS (memory, sql) reports a missing key as
// errors.Errorf("state [%s,%s] does not exist", namespace, id), and the hashicorp vault KVS
// reported it as errors.Errorf("state of id [%s] does not exist", id).
//
// Past tense for the vault one: it now reports absence the way IdentityExists describes, as a
// nil error with the destination untouched, so no backend in this tree returns that message
// any more. Its head stays listed because that backend is a module of its own, versioned
// independently of this one, so a deployment can pair this store with a release that still
// returns it.
//
// The leading space in notFoundSuffix is deliberate, not a stray character: it anchors the
// phrase on a word boundary, so an error whose final word merely ends in "does not exist"
// cannot satisfy the suffix test.
const notFoundSuffix = " does not exist"

var notFoundHeads = []string{"state [", "state of id ["}

// isNotFoundErr reports whether err is a KVS "key not found" error, as opposed to a real store
// failure. FSC has no absence sentinel yet, so both backends signal absence only through their
// error message (see notFoundSuffix), which leaves message matching as the only way to tell the
// two apart.
//
// The match is anchored at both ends rather than searching for the "does not exist" substring,
// because a store failure can carry that very phrase: a missing or not-yet-migrated table makes
// Postgres answer `relation "kvs" does not exist`, and classifying that as a miss is precisely
// the #2063 failure mode this function exists to prevent — GetWalletID would answer ("", nil)
// and the role Registry would create a duplicate wallet for an identity that already has one.
// Anchoring is what rules it out: both backends return their absence error unwrapped, so the
// message starts with one of notFoundHeads, whereas a failure is always wrapped
// ("failed retrieving state [ns,id]: ...") and can therefore never match, whatever its cause
// says.
//
// This is still fragile by nature — see the TODO in GetWalletID and issue #2063 — and must be
// replaced once FSC exposes a typed sentinel. The fragility is deliberately one-sided: being
// anchored, the match is tied to the exact wording of the two heads above, so a KVS whose
// absence error is worded differently has its miss propagated to the caller as a hard error.
// That is the safe direction to fail in. A GetWalletID that errors on an unbound identity
// makes the role Registry abort a wallet creation, loudly and recoverably; one that answers
// ("", nil) for an unreadable store makes it create a second wallet for an identity that
// already has one, which is #2063 and is neither. A widened match trades the first failure
// for the second, so the match is never to be loosened, only replaced by a sentinel.
//
// TestIsNotFoundErrMatchesLiveBackend is what keeps the wording honest: it classifies the
// error the in-tree KVS really returns, so a rewording upstream fails there rather than
// turning every miss into an error in production.
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

// getRaw reads the raw JSON stored under k. found=false, err=nil only for an authoritative miss:
// an isNotFoundErr error (FSC in-tree KVS) or an untouched destination (hashicorp vault KVS,
// which reports a missing id as a nil error). Every other error - a store failure, a cancelled
// or expired context, a corrupt entry - is returned to the caller unchanged, so that it is never
// mistaken for absence.
//
// This is the only presence check the stores in this package rely on for correctness. KVS.Exists
// and KVS.GetExisting are not used for that: they have no error return and report a failed read
// as "does not exist" (see KVS).
//
// A stored JSON null counts as found: the destination is a json.RawMessage, which records the
// literal rather than leaving the destination untouched. The returned bytes are a copy, so they
// never alias a cache inside the KVS.
// TODO(#2063): replace isNotFoundErr message-matching with the FSC sentinel once it lands.
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
