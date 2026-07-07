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
)

// Envelope is the EVM network envelope. It carries the token-request anchor (the SDK transaction
// id), and, once broadcast, the signed raw transaction and its Ethereum hash. Endorsements and the
// serialized StateDelta are added in Phase 4 when the endorsement flow lands.
type Envelope struct {
	Anchor    string `json:"anchor"`
	EthTxHash string `json:"eth_tx_hash,omitempty"`
	RawTx     []byte `json:"raw_tx,omitempty"`
}

// Compile-time assertion that Envelope satisfies the driver contract.
var _ driver.Envelope = (*Envelope)(nil)

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
