/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
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

// FromBytes deserialises raw ASN.1 DER into the receiver.
func (s *PolicySignature) FromBytes(raw []byte) error {
	_, err := asn1.Unmarshal(raw, s)

	return err
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
	if !v.evalNode(v.Policy, msg, sig.Signatures) {
		return errors.New("policy not satisfied")
	}

	return nil
}

// evalNode recursively evaluates the policy AST against the provided signatures.
func (v *PolicyVerifier) evalNode(node Node, msg []byte, sigs [][]byte) bool {
	switch n := node.(type) {
	case *RefNode:
		i := n.Index
		if i < 0 || i >= len(sigs) || len(sigs[i]) == 0 {
			return false
		}

		return v.Verifiers[i].Verify(msg, sigs[i]) == nil

	case *AndNode:
		return v.evalNode(n.Left, msg, sigs) && v.evalNode(n.Right, msg, sigs)

	case *OrNode:
		return v.evalNode(n.Left, msg, sigs) || v.evalNode(n.Right, msg, sigs)

	default:
		return false
	}
}
