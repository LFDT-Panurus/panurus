/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PolicySignature is the on-wire signature envelope for a policy identity.
// It carries one slot per component identity; slots for parties that did not
// sign are left nil/empty.  This allows OR policies to be satisfied by a
// strict subset of the component signers.
//
// Wire format: ASN.1 SEQUENCE OF OCTET STRING (mirrors MultiSignature).
type PolicySignature struct {
	Signatures [][]byte
}

// Bytes serialises the PolicySignature to ASN.1 DER.
func (s *PolicySignature) Bytes() ([]byte, error) {
	return asn1.Marshal(*s)
}

// FromBytes deserialises raw ASN.1 DER into the receiver. It rejects trailing
// bytes after the encoded value: raw is peer-supplied, and the envelope itself
// is always produced by our own canonical asn1.Marshal, so leftover bytes only
// ever mean a mangled or deliberately padded signature. (This tightens the
// PolicySignature *envelope* only — the individual signatures it carries are
// parsed by their own verifiers, which must stay lenient for external
// signers/HSMs.)
func (s *PolicySignature) FromBytes(raw []byte) error {
	return marshal.UnmarshalCanonicalDER(raw, s)
}

// JoinSignatures builds a PolicySignature from a map of per-identity
// signatures.  Identities not present in sigmas receive a nil entry,
// which is valid as long as the policy does not require them.
// The order of the entries matches the order of identities.
func JoinSignatures(identities []token.Identity, sigmas map[string][]byte) ([]byte, error) {
	signatures := make([][]byte, len(identities))
	for k, id := range identities {
		if sig, ok := sigmas[id.UniqueID()]; ok {
			signatures[k] = sig
		}
		// absent entry stays nil — valid for OR branches
	}

	return (&PolicySignature{Signatures: signatures}).Bytes()
}

// PolicyVerifier verifies a PolicySignature against a parsed policy AST.
// It implements driver.Verifier.
//
// Verification walks the policy AST:
//   - RefNode{i}: sigs[i] must be non-empty and Verifiers[i].Verify must succeed.
//   - AndNode:    both sub-trees must verify successfully.
//   - OrNode:     at least one sub-tree must verify successfully.
//
// This means a valid PolicySignature need only carry signatures for the
// identities actually required by the satisfied policy branch.
type PolicyVerifier struct {
	// Policy is the parsed boolean AST, produced by Parse.
	Policy Node
	// Verifiers is indexed by $N; each entry verifies the corresponding
	// component identity's individual signature.
	Verifiers []driver.Verifier
}

// Verify implements driver.Verifier.
// sigBytes must be a PolicySignature ASN.1 DER blob produced by JoinSignatures.
func (v *PolicyVerifier) Verify(msg, sigBytes []byte) error {
	sig := &PolicySignature{}
	if err := sig.FromBytes(sigBytes); err != nil {
		return errors.Wrap(err, "failed to unmarshal policy signature")
	}
	if len(sig.Signatures) != len(v.Verifiers) {
		return errors.Errorf("policy signature has [%d] slots, expected [%d]",
			len(sig.Signatures), len(v.Verifiers))
	}
	// memo caches each index's verification result for the duration of this
	// Verify call: a $N reference is checked against the same (msg, sigs[N])
	// no matter how many times it appears in the policy, so the potentially
	// expensive Verifiers[N].Verify is invoked at most once per index.
	memo := make([]refResult, len(sig.Signatures))
	if !v.evalNode(v.Policy, msg, sig.Signatures, memo) {
		return errors.New("policy not satisfied")
	}

	return nil
}

// refResult is the memoised outcome of verifying a single component index.
type refResult int8

const (
	refUnknown refResult = iota // not yet verified in this Verify call
	refPass                     // verification succeeded
	refFail                     // verification failed (absent slot or bad signature)
)

// evalNode recursively evaluates the policy AST against the provided signatures.
// memo is indexed by component index ($N) and caches per-index verification
// results across the whole traversal; it must have one entry per signature slot.
func (v *PolicyVerifier) evalNode(node Node, msg []byte, sigs [][]byte, memo []refResult) bool {
	switch n := node.(type) {
	case *RefNode:
		return v.evalRef(n.Index, msg, sigs, memo)

	case *AndNode:
		return v.evalNode(n.Left, msg, sigs, memo) && v.evalNode(n.Right, msg, sigs, memo)

	case *OrNode:
		return v.evalNode(n.Left, msg, sigs, memo) || v.evalNode(n.Right, msg, sigs, memo)

	default:
		return false
	}
}

// evalRef verifies the component identity at index i, reusing a previously
// computed result for the same index when one is available.
func (v *PolicyVerifier) evalRef(i int, msg []byte, sigs [][]byte, memo []refResult) bool {
	if i < 0 || i >= len(sigs) || len(sigs[i]) == 0 {
		return false
	}
	if memo[i] != refUnknown {
		return memo[i] == refPass
	}

	ok := v.Verifiers[i].Verify(msg, sigs[i]) == nil
	if ok {
		memo[i] = refPass
	} else {
		memo[i] = refFail
	}

	return ok
}
