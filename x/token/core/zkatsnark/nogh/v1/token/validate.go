/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Byte lengths used by structural validation. These mirror the constants in
// the validator package but are intentionally redeclared here so the token
// package stays free of import cycles.
const (
	fieldElementLen = 32 // one Fr element, canonical encoding (BLS12-381 / BN254)
	signatureLen    = 96 // R.X (32) || R.Y (32) || S (32)
	fpElementLen    = 48 // one BLS12-381 base-field (Fp) element
)

// Validate performs a structural self-check on the OutputDescription.
// It verifies that all cryptographic fields (CommitmentOut, ValueCommitOutX,
// ValueCommitOutY, TypeCommitment) are exactly 32 bytes and OutputProof is non-empty.
func (o *OutputDescription) Validate() error {
	if len(o.CommitmentOut) != fieldElementLen {
		return errors.Errorf("CommitmentOut must be %d bytes, got %d", fieldElementLen, len(o.CommitmentOut))
	}
	if len(o.ValueCommitOutX) != fieldElementLen {
		return errors.Errorf("ValueCommitOutX must be %d bytes, got %d", fieldElementLen, len(o.ValueCommitOutX))
	}
	if len(o.ValueCommitOutY) != fieldElementLen {
		return errors.Errorf("ValueCommitOutY must be %d bytes, got %d", fieldElementLen, len(o.ValueCommitOutY))
	}
	if len(o.TypeCommitment) != fieldElementLen {
		return errors.Errorf("TypeCommitment must be %d bytes, got %d", fieldElementLen, len(o.TypeCommitment))
	}
	if len(o.OutputProof) == 0 {
		return errors.New("OutputProof must not be empty")
	}

	return nil
}

// Validate performs a structural self-check on the SpendDescription.
// It verifies that all cryptographic fields (CommitmentIn, ValueCommitInX,
// ValueCommitInY, TypeCommitment) are exactly 32 bytes and SpendProof is non-empty.
func (s *SpendDescription) Validate() error {
	if len(s.CommitmentIn) != fieldElementLen {
		return errors.Errorf("CommitmentIn must be %d bytes, got %d", fieldElementLen, len(s.CommitmentIn))
	}
	if len(s.ValueCommitInX) != fieldElementLen {
		return errors.Errorf("ValueCommitInX must be %d bytes, got %d", fieldElementLen, len(s.ValueCommitInX))
	}
	if len(s.ValueCommitInY) != fieldElementLen {
		return errors.Errorf("ValueCommitInY must be %d bytes, got %d", fieldElementLen, len(s.ValueCommitInY))
	}
	if len(s.TypeCommitment) != fieldElementLen {
		return errors.Errorf("TypeCommitment must be %d bytes, got %d", fieldElementLen, len(s.TypeCommitment))
	}
	if len(s.SpendProof) == 0 {
		return errors.New("SpendProof must not be empty")
	}

	return nil
}
