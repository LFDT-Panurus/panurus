/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"bytes"
	"fmt"
)

// ID identifies a token as a function of the identifier of the transaction (issue, transfer)
// that created it and its index in that transaction
type ID struct {
	// TxId is the transaction ID of the transaction that created the token
	TxId string `json:"tx_id,omitempty" protobuf:"bytes,1,opt,name=tx_id,json=txId,proto3"`
	// Index is the index of the token in the transaction that created it
	Index uint64 `json:"index,omitempty" protobuf:"varint,2,opt,name=index,proto3"`
}

func (id ID) Equal(right ID) bool {
	return id.TxId == right.TxId && id.Index == right.Index
}

func (id ID) String() string {
	return fmt.Sprintf("[%s:%d]", id.TxId, id.Index)
}

type (
	// Type is the currency, e.g. USD
	Type string
	// Format is the encoding of a token on the ledger, e.g. fabtoken, comm.
	// It is a many-to-many relationship with the token driver,
	// i.e. a token driver can support multiple formats (e.g. fabtoken1, fabtoken2),
	// but a format can also be supported by multiple drivers (e.g. zkat, zkatlog).
	Format string
)

// Token is the result of issue and transfer transactions
type Token struct {
	// Owner is the token owner
	Owner []byte `json:"owner,omitempty" protobuf:"bytes,1,opt,name=owner,proto3"`
	// Type is the type of the token
	Type Type `json:"type,omitempty" protobuf:"bytes,2,opt,name=type,proto3"`
	// Quantity is the number of units of Type carried in the token.
	// It is encoded as a string containing a non-negative integer. The value is parsed
	// with big.Int base-0 auto-detection, so the base is inferred from the string's prefix:
	// decimal by default, hexadecimal with a "0x"/"0X" prefix, octal with a "0o"/"0O" prefix
	// (or a leading "0"), and binary with a "0b"/"0B" prefix.
	Quantity string `json:"quantity,omitempty" protobuf:"bytes,3,opt,name=quantity,proto3"`
}

// Clone returns a full, independent copy of the token. The returned token
// shares no mutable state with the receiver: the Owner byte slice is copied,
// so mutating either token's Owner (or replacing any field) does not affect
// the other. Type and Quantity are strings and are therefore immutable.
func (t *Token) Clone() *Token {
	return &Token{
		Owner:    bytes.Clone(t.Owner),
		Type:     t.Type,
		Quantity: t.Quantity,
	}
}

type IssuedToken struct {
	// Id is used to uniquely identify the token in the ledger
	Id ID `json:"id" protobuf:"bytes,1,opt,name=id,proto3"`
	// Owner is the token owner
	Owner []byte `json:"owner,omitempty" protobuf:"bytes,1,opt,name=owner,proto3"`
	// Type is the type of the token
	Type Type `json:"type,omitempty" protobuf:"bytes,2,opt,name=type,proto3"`
	// Quantity represents the number of units of Type that this unspent token holds.
	// It is formatted in decimal representation
	Quantity string `json:"quantity,omitempty" protobuf:"bytes,3,opt,name=quantity,proto3"`
	// Issuer is the issuer of this token
	Issuer []byte
}

type IssuedTokens struct {
	// Tokens is an array of UnspentToken
	Tokens []*IssuedToken `json:"tokens,omitempty" protobuf:"bytes,1,rep,name=tokens,proto3"`
}

func (it *IssuedTokens) Sum(precision uint64) Quantity {
	sum := NewZeroQuantity(precision)
	for _, token := range it.Tokens {
		q, err := ToQuantity(token.Quantity, precision)
		if err != nil {
			panic(err)
		}
		sum, err = sum.Add(q)
		if err != nil {
			panic(err)
		}
	}

	return sum
}

func (it *IssuedTokens) ByType(typ Type) *IssuedTokens {
	res := &IssuedTokens{Tokens: []*IssuedToken{}}
	for _, token := range it.Tokens {
		if token.Type == typ {
			res.Tokens = append(res.Tokens, token)
		}
	}

	return res
}

func (it *IssuedTokens) Count() int {
	return len(it.Tokens)
}

// UnspentTokenInWallet models an unspent token owner solely by a given wallet
type UnspentTokenInWallet struct {
	// Id is used to uniquely identify the token in the ledger
	Id ID
	// WalletID is the ID of the wallet owning this token
	WalletID string
	// Type is the type of the token
	Type Type
	// Quantity represents the number of units of Type that this unspent token holds.
	Quantity string
}

type LedgerToken struct {
	// ID is used to uniquely identify the token in the ledger
	ID            ID
	Format        Format
	Token         []byte
	TokenMetadata []byte
}

func (t LedgerToken) Equal(right LedgerToken) bool {
	return t.ID.Equal(right.ID) &&
		bytes.Equal([]byte(t.Format), []byte(right.Format)) &&
		bytes.Equal(t.Token, right.Token) &&
		bytes.Equal(t.TokenMetadata, right.TokenMetadata)
}

// UnspentToken models an unspent token
type UnspentToken struct {
	// Id is used to uniquely identify the token in the ledger
	Id ID
	// Owner is the token owner
	Owner []byte
	// Type is the type of the token
	Type Type
	// Quantity represents the number of units of Type that this unspent token holds.
	Quantity string
}

func (ut UnspentToken) String() string {
	return ut.Id.String()
}

// UnspentTokens is used to hold the output of ListRequest
type UnspentTokens struct {
	// Tokens is an array of UnspentToken
	Tokens []*UnspentToken `json:"tokens,omitempty" protobuf:"bytes,1,rep,name=tokens,proto3"`
}

func (it *UnspentTokens) Count() int {
	return len(it.Tokens)
}

func (it *UnspentTokens) Sum(precision uint64) Quantity {
	sum := NewZeroQuantity(precision)
	for _, token := range it.Tokens {
		q, err := ToQuantity(token.Quantity, precision)
		if err != nil {
			panic(err)
		}
		sum, err = sum.Add(q)
		if err != nil {
			panic(err)
		}
	}

	return sum
}

func (it *UnspentTokens) ByType(typ Type) *UnspentTokens {
	res := &UnspentTokens{Tokens: []*UnspentToken{}}
	for _, token := range it.Tokens {
		if token.Type == typ {
			res.Tokens = append(res.Tokens, token)
		}
	}

	return res
}

// At returns the unspent token at position i.
// No boundary checks are performed.
func (it *UnspentTokens) At(i int) *UnspentToken {
	return it.Tokens[i]
}
