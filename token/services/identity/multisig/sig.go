/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// MultiSignature represents a multi-signature
// It is a sequence of signatures from different identities on the same message.
// The order of the signatures is the same as the order of the identities.
type MultiSignature struct {
	Signatures [][]byte
}

func (m *MultiSignature) Bytes() ([]byte, error) {
	return asn1.Marshal(*m)
}

func (m *MultiSignature) FromBytes(raw []byte) error {
	_, err := asn1.Unmarshal(raw, m)

	return err
}

// JoinSignatures joins the signatures of the given identities into a single signature
// The order of the signatures is the same as the order of the identities.
func JoinSignatures(identities []token.Identity, sigmas map[string][]byte) ([]byte, error) {
	signatures := make([][]byte, len(identities))
	for k, identity := range identities {
		uid := identity.UniqueID()
		sigma, ok := sigmas[uid]
		if !ok {
			return nil, errors.Errorf("signature for identity [%s] is missing", uid)
		}
		signatures[k] = sigma
	}
	sig := &MultiSignature{
		Signatures: signatures,
	}

	return sig.Bytes()
}

// Verifier is a multi-signature verifier that verifies a multi-signature.
// It is composed of a list of verifiers, one for each identity that signed the message.
// The order of the verifiers is the same as the order of the identities.
type Verifier struct {
	Verifiers []driver.Verifier
}

func (v *Verifier) Verify(msg, raw []byte) error {
	if len(v.Verifiers) == 0 {
		return errors.New("multisig verifier has no members")
	}
	sig := &MultiSignature{}
	err := sig.FromBytes(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal multisig [%s]", utils.Hashable(raw))
	}
	if len(v.Verifiers) != len(sig.Signatures) {
		return errors.Errorf("invalid multisig: expect [%d] signatures, but received [%d]", len(v.Verifiers), len(sig.Signatures))
	}
	for k, ver := range v.Verifiers {
		if err = ver.Verify(msg, sig.Signatures[k]); err != nil {
			return errors.Errorf("invalid multisig: signature at index [%d] does not verify", k)
		}
	}

	return nil
}
