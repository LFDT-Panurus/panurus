/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package integrity

import (
	"bytes"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrEmptyIdentity is returned when an empty identity is about to be stored
	// or looked up.
	ErrEmptyIdentity = errors.New("empty identity")
	// ErrIdentityMismatch is returned when a record retrieved by identity hash
	// turns out to belong to a different identity than the one requested.
	ErrIdentityMismatch = errors.New("stored identity does not match requested identity")
)

// CheckIdentity rejects an empty identity.
//
// This is not a style guard. Identity rows are keyed by
// [github.com/hyperledger-labs/fabric-smart-client/platform/common/services/identity.Identity.UniqueID],
// which maps the empty identity to the constant string "<empty>" rather than to
// a hash. Every empty identity therefore shares one row key: storing signer
// info or audit info for an empty identity writes it to a well-known key that a
// later empty-identity lookup — from an unmarshalling slip, a zero-valued
// struct field, or an attacker-supplied empty identity — will read back as if
// it belonged to whoever asked. Refusing empty identities at the boundary keeps
// that key unoccupied.
func CheckIdentity(id []byte) error {
	if len(id) == 0 {
		return ErrEmptyIdentity
	}

	return nil
}

// CheckIdentityMatch verifies that a record retrieved by identity hash actually
// belongs to the identity that was requested, by comparing the identity stored
// alongside the record against the requested one.
//
// Identity records are addressed by hash, so callers get back whatever row the
// hash of their identity lands on. Comparing the stored identity turns the
// store's promise from "this is the row your hash pointed at" into "this
// belongs to the identity you named": it catches a row whose identity and
// identity_hash columns disagree — a corrupted or out-of-band-modified row, or
// one written under the shared "<empty>" key described on CheckIdentity — before
// the signer info or audit info reaches a caller that will use it to sign or to
// attribute a transaction.
//
// stored may be empty, which means the store holds no identity for the row; that
// is reported as a mismatch, since the record then cannot be attributed to
// anyone.
func CheckIdentityMatch(requested []byte, stored []byte) error {
	if len(requested) == 0 {
		return ErrEmptyIdentity
	}
	if !bytes.Equal(requested, stored) {
		return errors.Wrapf(ErrIdentityMismatch, "requested identity of %d bytes, stored identity of %d bytes", len(requested), len(stored))
	}

	return nil
}
