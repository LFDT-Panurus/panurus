/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"math/big"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

const (
	// DomainName and DomainVersion identify the signing domain and must match the Solidity contract.
	DomainName    = "Panurus"
	DomainVersion = "1"
)

// domainTypeHash = keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)").
var domainTypeHash = crypto.Keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

// Domain binds endorser signatures to a specific chain and TokenState contract, so a signature cannot
// be replayed on another chain or against another contract.
type Domain struct {
	ChainID           *big.Int
	VerifyingContract client.Address
}

// Separator returns the EIP-712 domain separator:
// keccak256(domainTypeHash || keccak256(name) || keccak256(version) || chainId || verifyingContract).
//
// A nil ChainID is treated as zero rather than panicking; the configuration layer validates that the
// chain ID is set, and a zero here yields a separator that will not match the contract, surfacing the
// misconfiguration as a signature-verification failure rather than a crash during signing.
func (d Domain) Separator() [32]byte {
	chainID := d.ChainID
	if chainID == nil {
		chainID = big.NewInt(0)
	}

	buf := make([]byte, 0, 5*wordLen)
	buf = append(buf, domainTypeHash...)
	buf = append(buf, crypto.Keccak256([]byte(DomainName))...)
	buf = append(buf, crypto.Keccak256([]byte(DomainVersion))...)
	buf = append(buf, bigWord(chainID)...)
	buf = append(buf, addressWord(d.VerifyingContract)...)

	return crypto.Keccak256Hash(buf)
}
