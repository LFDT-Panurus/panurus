/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade

import (
	errors2 "errors"

	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Proof represents the proof of ownership for tokens being upgraded.
type Proof struct {
	// Challenge is the random challenge provided by the system.
	Challenge driver.TokensUpgradeChallenge
	// Tokens is the list of tokens to be upgraded.
	Tokens []token.LedgerToken
	// Signatures is the list of signatures for each token in the list.
	Signatures []Signature
}

// Serialize marshals the Proof into a JSON byte slice.
func (p *Proof) Serialize() ([]byte, error) {
	return json.Marshal(p)
}

// Deserialize unmarshals a JSON byte slice into the Proof.
func (p *Proof) Deserialize(raw []byte) error {
	return json.Unmarshal(raw, p)
}

// SHA256Digest computes the SHA256 hash of the challenge and tokens.
func SHA256Digest(ch driver.TokensUpgradeChallenge, tokens []token.LedgerToken) ([]byte, error) {
	h := utils.NewSHA256Hasher()
	err := h.AddBytes(ch)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to write challenge to hash")
	}
	for _, token := range tokens {
		if err := errors2.Join(
			h.AddString(token.ID.TxId),
			h.AddUInt64(token.ID.Index),
			h.AddBytes(token.Token),
			h.AddBytes(token.TokenMetadata),
			h.AddString(string(token.Format)),
		); err != nil {
			return nil, errors.Wrapf(err, "failed to write token to hash")
		}
	}

	return h.Digest(), nil
}
