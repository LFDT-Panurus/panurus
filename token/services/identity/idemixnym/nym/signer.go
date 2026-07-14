/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type Signature struct {
	Creator   []byte
	Signature []byte
}

type Signer struct {
	Creator []byte
	Signer  driver.Signer
}

func (s *Signer) Sign(message []byte) ([]byte, error) {
	signature, err := s.Signer.Sign(message)
	if err != nil {
		return nil, err
	}

	return asn1.Marshal(Signature{
		Creator:   s.Creator,
		Signature: signature,
	})
}

// Verifier verifies the signature of a message under a given commitment of an Enrollment ID
type Verifier struct {
	NymEID []byte // This is the PK against which the verifier verifies signature
	Backed backedDeserializer
}

func (v *Verifier) Verify(message, sigma []byte) error {
	sig := &Signature{}
	_, err := asn1.Unmarshal(sigma, sig)
	if err != nil {
		return errors.Wrapf(
			err,
			"failed to verify idemix-plus signature [len=%d][%s]",
			len(sigma),
			utils.Hashable(sigma),
		)
	}

	id, err := v.Backed.DeserializeAgainstNymEID(sig.Creator, v.NymEID)
	if err != nil {
		return errors.Wrapf(err, "failed to get idemix deserializer")
	}

	return id.Identity.Verify(message, sig.Signature)
}
