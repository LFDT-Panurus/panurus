/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides token stream operations for filtering, aggregating, and querying token collections.
package token

import (
	"context"
	"fmt"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type QueryService interface {
	IsMine(ctx context.Context, id *token.ID) (bool, error)
}

// Output models the output of a token action
type Output struct {
	token.Token
	// ActionIndex is the index of the action that created this output
	ActionIndex int
	// Index is the absolute position of this output in the token request
	Index uint64
	// Owner is the identity of the owner of this output
	Owner Identity
	// OwnerAuditInfo contains the audit information of the output's owner
	OwnerAuditInfo []byte
	// EnrollmentID is the enrollment ID of the owner of this output
	EnrollmentID string
	// RevocationHandler is the revocation handler of the owner of this output
	RevocationHandler string
	// Type is the type of token
	Type token.Type
	// Quantity is the quantity of tokens
	Quantity token.Quantity
	// LedgerOutput contains the output as it appears on the ledger
	LedgerOutput []byte
	// LedgerOutputFormat is the output type
	LedgerOutputFormat token.Format
	// LedgerOutputMetadata is the metadata of the output
	LedgerOutputMetadata []byte
	// Issuer is the identity of the issuer of this output, if any
	Issuer driver.Identity
}

func (o Output) ID(anchor RequestAnchor) *token.ID {
	return &token.ID{TxId: string(anchor), Index: o.Index}
}

// OutputStream models a stream over a set of outputs (Output).
type OutputStream struct {
	Precision uint64
	outputs   []*Output
}

// NewOutputStream creates a new OutputStream for the passed outputs and Precision.
func NewOutputStream(outputs []*Output, precision uint64) *OutputStream {
	return &OutputStream{outputs: outputs, Precision: precision}
}

// Filter filters the OutputStream to only include outputs that match the passed predicate.
func (o *OutputStream) Filter(f func(t *Output) bool) *OutputStream {
	var filtered []*Output
	for _, output := range o.outputs {
		if f(output) {
			filtered = append(filtered, output)
		}
	}

	return &OutputStream{outputs: filtered, Precision: o.Precision}
}

// ByRecipient filters the OutputStream to only include outputs that match the passed recipient.
func (o *OutputStream) ByRecipient(id Identity) *OutputStream {
	return o.Filter(func(t *Output) bool {
		return id.Equal(t.Owner)
	})
}

// ByType filters the OutputStream to only include outputs that match the passed type.
func (o *OutputStream) ByType(typ token.Type) *OutputStream {
	return o.Filter(func(t *Output) bool {
		return t.Type == typ
	})
}

// IsRedeem returns true if this output is a redeem, i.e., it has no owner.
func (o *Output) IsRedeem() bool {
	return len(o.Owner) == 0
}

// ByRedeem filters the OutputStream to only include outputs that are redeems.
func (o *OutputStream) ByRedeem() *OutputStream {
	return o.Filter(func(t *Output) bool {
		return t.IsRedeem()
	})
}

// Issuers returns the distinct issuer identities of the outputs in the OutputStream.
func (o *OutputStream) Issuers() []driver.Identity {
	duplicates := map[string]any{}
	var issuers []driver.Identity
	for _, output := range o.outputs {
		if output.Issuer.IsNone() {
			continue
		}
		key := output.Issuer.UniqueID()
		if _, ok := duplicates[key]; !ok {
			issuers = append(issuers, output.Issuer)
			duplicates[key] = true
		}
	}

	return issuers
}

// Outputs returns the outputs of the OutputStream.
func (o *OutputStream) Outputs() []*Output {
	return o.outputs
}

// Count returns the number of outputs in the OutputStream.
func (o *OutputStream) Count() int {
	return len(o.outputs)
}

// Sum returns the sum of the quantity of all outputs in the OutputStream.
func (o *OutputStream) Sum() *big.Int {
	sum := big.NewInt(0)
	for _, input := range o.outputs {
		sum = sum.Add(sum, input.Quantity.ToBigInt())
	}

	return sum
}

// At returns the output at the passed index.
func (o *OutputStream) At(i int) *Output {
	return o.outputs[i]
}

// ByEnrollmentID filters to only include outputs that match the passed enrollment ID.
func (o *OutputStream) ByEnrollmentID(id string) *OutputStream {
	return o.Filter(func(t *Output) bool {
		return t.EnrollmentID == id
	})
}

// EnrollmentIDs returns the enrollment IDs of the outputs in the OutputStream.
func (o *OutputStream) EnrollmentIDs() []string {
	duplicates := map[string]any{}
	var eIDs []string
	for _, output := range o.outputs {
		if len(output.EnrollmentID) == 0 {
			continue
		}
		if _, ok := duplicates[output.EnrollmentID]; !ok {
			eIDs = append(eIDs, output.EnrollmentID)
			duplicates[output.EnrollmentID] = true
		}
	}

	return eIDs
}

// TokenTypes returns the token types of the outputs in the OutputStream.
func (o *OutputStream) TokenTypes() []token.Type {
	duplicates := map[token.Type]any{}
	var types []token.Type
	for _, output := range o.outputs {
		if _, ok := duplicates[output.Type]; !ok {
			types = append(types, output.Type)
			duplicates[output.Type] = true
		}
	}

	return types
}

// RevocationHandles returns the Revocation Handles of the owners of the outputs.
// It might be empty, if not available.
func (o *OutputStream) RevocationHandles() []string {
	duplicates := map[string]any{}
	var rIDs []string
	for _, output := range o.outputs {
		rh := output.RevocationHandler
		if len(rh) == 0 {
			continue
		}
		_, ok := duplicates[rh]
		if !ok {
			rIDs = append(rIDs, rh)
			duplicates[rh] = true
		}
	}

	return rIDs
}

// String returns a string representation of the input stream
func (o *OutputStream) String() string {
	return fmt.Sprintf("[%v]", o.outputs)
}

// Input models an input of a token action
type Input struct {
	ActionIndex       int
	Id                *token.ID
	Owner             Identity
	OwnerAuditInfo    []byte
	EnrollmentID      string
	RevocationHandler string
	Type              token.Type
	Quantity          token.Quantity
}

// InputStream models a stream over a set of inputs (Input).
type InputStream struct {
	qs        QueryService
	inputs    []*Input
	precision uint64
}

// NewInputStream creates a new InputStream for the passed inputs and query service.
func NewInputStream(qs QueryService, inputs []*Input, precision uint64) *InputStream {
	return &InputStream{qs: qs, inputs: inputs, precision: precision}
}

// Filter returns a new InputStream with only the inputs that satisfy the predicate
func (is *InputStream) Filter(f func(t *Input) bool) *InputStream {
	var filtered []*Input
	for _, item := range is.inputs {
		if f(item) {
			filtered = append(filtered, item)
		}
	}

	return &InputStream{inputs: filtered, precision: is.precision}
}

// Count returns the number of inputs in the stream
func (is *InputStream) Count() int {
	return len(is.inputs)
}

// Owners returns a list of identities that own the tokens in the stream
func (is *InputStream) Owners() *OwnerStream {
	ownerMap := map[string]bool{}
	var owners []string
	for _, input := range is.inputs {
		_, ok := ownerMap[input.Owner.UniqueID()]
		if ok {
			continue
		}
		ownerMap[input.Owner.UniqueID()] = true
		owners = append(owners, input.Owner.UniqueID())
	}

	return &OwnerStream{owners: owners}
}

// IsAnyMine returns true if any of the inputs are mine
func (is *InputStream) IsAnyMine(ctx context.Context) (bool, error) {
	for _, input := range is.inputs {
		mine, err := is.qs.IsMine(ctx, input.Id)
		if err != nil {
			return false, errors.WithMessagef(err, "failed to query the vault")
		}
		if mine {
			return true, nil
		}
	}

	return false, nil
}

// String returns a string representation of the input stream
func (is *InputStream) String() string {
	return fmt.Sprintf("[%v]", is.inputs)
}

// At returns the input at the given index.
func (is *InputStream) At(i int) *Input {
	return is.inputs[i]
}

// IDs returns the IDs of the inputs.
func (is *InputStream) IDs() []*token.ID {
	var res []*token.ID
	for _, input := range is.inputs {
		res = append(res, input.Id)
	}

	return res
}

// EnrollmentIDs returns the enrollment IDs of the owners of the inputs.
// It might be empty, if not available.
func (is *InputStream) EnrollmentIDs() []string {
	duplicates := map[string]any{}
	var eIDs []string
	for _, input := range is.inputs {
		if len(input.EnrollmentID) == 0 {
			continue
		}

		_, ok := duplicates[input.EnrollmentID]
		if !ok {
			eIDs = append(eIDs, input.EnrollmentID)
			duplicates[input.EnrollmentID] = true
		}
	}

	return eIDs
}

// RevocationHandles returns the Revocation Handles of the owners of the inputs.
// It might be empty, if not available.
func (is *InputStream) RevocationHandles() []string {
	duplicates := map[string]any{}
	var rIDs []string
	for _, input := range is.inputs {
		rh := input.RevocationHandler
		if len(rh) == 0 {
			continue
		}
		_, ok := duplicates[rh]
		if !ok {
			rIDs = append(rIDs, rh)
			duplicates[rh] = true
		}
	}

	return rIDs
}

// TokenTypes returns the token types of the inputs.
func (is *InputStream) TokenTypes() []token.Type {
	duplicates := map[token.Type]any{}
	var types []token.Type
	for _, input := range is.inputs {
		_, ok := duplicates[input.Type]
		if !ok {
			types = append(types, input.Type)
			duplicates[input.Type] = true
		}
	}

	return types
}

// ByEnrollmentID filters by enrollment ID.
func (is *InputStream) ByEnrollmentID(id string) *InputStream {
	return is.Filter(func(t *Input) bool {
		return t.EnrollmentID == id
	})
}

// ByType filters by token type.
func (is *InputStream) ByType(tokenType token.Type) *InputStream {
	return is.Filter(func(t *Input) bool {
		return t.Type == tokenType
	})
}

// Sum returns the sum of the quantities of the inputs.
func (is *InputStream) Sum() *big.Int {
	sum := big.NewInt(0)
	for _, input := range is.inputs {
		sum = sum.Add(sum, input.Quantity.ToBigInt())
	}

	return sum
}

// Inputs returns the inputs in this InputStream.
func (is *InputStream) Inputs() []*Input {
	return is.inputs
}

type OwnerStream struct {
	owners []string
}

func NewOwnerStream(owners []string) *OwnerStream {
	return &OwnerStream{owners: owners}
}

func (s *OwnerStream) Count() int {
	return len(s.owners)
}
