/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

// SpendDescription is the public wire-format record for one token being
// consumed as an input. The commitment being spent, its value commitment,
// and its type are all public; the value, randomness, and value-commitment
// randomness that open them are not.
type SpendDescription struct {
	CommitmentIn   []byte // 32 bytes
	ValueCommitInX []byte // 32 bytes
	ValueCommitInY []byte // 32 bytes
	TokenType      []byte // 32 bytes
	SpendProof     []byte // 192 bytes: Groth16 proof for SpendCircuit
}

// TransferAction represents consuming one or more existing tokens and
// creating one or more new tokens, with value conserved across the two
// sets.
//
// This layer does not yet know whether TransferAction needs to satisfy a
// broader Panurus driver.Action interface, or whether the entity that
// produces BindingSignature needs to be registered through an existing
// "extra signer" mechanism (per Angelo's review comment) rather than simply
// being a byte slice on this struct. Both are open integration questions —
// everything else here is complete and correct independent of how they
// resolve.
type TransferAction struct {
	TokenType        string
	Inputs           []SpendDescription
	Outputs          []OutputDescription
	BindingSignature []byte // 96 bytes: R.X || R.Y || S
}
