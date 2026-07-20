/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

// OutputDescription is the public wire-format record for one newly created
// token. Everything here is safe to publish, the note secrets (value,
// type, randomness) that this commitment opens to are never included.
type OutputDescription struct {
	CommitmentOut   []byte // 32 bytes: MiMC(value, type, randomness)
	ValueCommitOutX []byte // 32 bytes: Jubjub X coordinate of cv
	ValueCommitOutY []byte // 32 bytes: Jubjub Y coordinate of cv
	TokenType       []byte // 32 bytes: canonical field-element encoding of type
	OutputProof     []byte // 192 bytes: Groth16 proof for OutputCircuit
	Recipient       []byte // intended recipient identity
}

// IssueAction represents the creation of one or more new tokens.
//
// There are no SpendDescriptions — issuance consumes nothing. The binding
// signature's signing key is bsk = −Σrcv_out (no input RCVs to add), and it
// attests that every output's value commitment is correctly formed, not
// that any conservation equation holds — there is nothing to conserve
// against for freshly minted tokens.
//
// Issuance authorization itself (who is allowed to mint tokens of this
// type) is not represented here — that is expected to come from whatever
// Panurus's existing issuer-authorization mechanism is (Issuers() on
// PublicParameters, or an equivalent endorsement-policy check at the
// network layer), not from anything in this struct.
type IssueAction struct {
	Issuer           []byte
	TokenType        string
	Outputs          []OutputDescription
	BindingSignature []byte // 96 bytes: R.X || R.Y || S
}
