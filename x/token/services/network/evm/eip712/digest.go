/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// Digest returns the EIP-712 signing digest for a StateDelta:
//
//	keccak256(0x19 || 0x01 || domainSeparator || hashStruct(delta))
//
// Endorsers sign this digest with their secp256k1 key, and the TokenState contract recomputes it
// identically from the typed delta before verifying the signatures.
func Digest(domain Domain, d *statedelta.StateDelta) [32]byte {
	sep := domain.Separator()
	hs := HashStruct(d)

	buf := make([]byte, 0, 2+2*wordLen)
	buf = append(buf, 0x19, 0x01)
	buf = append(buf, sep[:]...)
	buf = append(buf, hs[:]...)

	return crypto.Keccak256Hash(buf)
}
