/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"encoding/json"
	"fmt"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// Envelope is the EVM network envelope. It carries the token-request anchor (the SDK transaction
// id), the endorsed StateDelta and the collected endorsements produced by RequestApproval, and, once
// broadcast, the signed raw transaction and its Ethereum hash.
//
// The lifecycle is two stages. RequestApproval fills Delta and Endorsements: the delta every endorser
// signed and the quorum of signatures over its EIP-712 digest. Broadcast (Week 5) ABI-encodes
// applyStateDelta(Delta, Endorsements) into a signed transaction and fills RawTx and EthTxHash.
type Envelope struct {
	Anchor       string                 `json:"anchor"`
	Delta        *statedelta.StateDelta `json:"delta,omitempty"`
	Endorsements [][]byte               `json:"endorsements,omitempty"`
	EthTxHash    string                 `json:"eth_tx_hash,omitempty"`
	RawTx        []byte                 `json:"raw_tx,omitempty"`
}

// Compile-time assertion that Envelope satisfies the driver contract.
var _ driver.Envelope = (*Envelope)(nil)

// NewApprovedEnvelope builds the envelope RequestApproval returns once endorsement has assembled a
// quorum: the anchor, the endorsed delta, and the signatures over its digest. RawTx and EthTxHash
// stay empty until Broadcast submits it. Kept in primitive terms so the root package does not depend
// on the endorsement package; the driver maps an endorsement result onto these arguments.
func NewApprovedEnvelope(anchor string, delta *statedelta.StateDelta, endorsements [][]byte) *Envelope {
	return &Envelope{Anchor: anchor, Delta: delta, Endorsements: endorsements}
}

// Bytes marshals the envelope to bytes.
func (e *Envelope) Bytes() ([]byte, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal evm envelope")
	}

	return raw, nil
}

// FromBytes unmarshals the envelope from bytes.
func (e *Envelope) FromBytes(raw []byte) error {
	if err := json.Unmarshal(raw, e); err != nil {
		return errors.Wrapf(err, "failed to unmarshal evm envelope")
	}

	return nil
}

// TxID returns the token-request anchor identifying this envelope.
func (e *Envelope) TxID() string { return e.Anchor }

// String returns a human-readable representation of the envelope.
func (e *Envelope) String() string {
	return fmt.Sprintf("evm-envelope[anchor=%s, ethTx=%s]", e.Anchor, e.EthTxHash)
}
