/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// TransferOptions models the options that can be passed to the transfer command
type TransferOptions struct {
	// Attributes is a container of generic options that might be driver specific
	Attributes map[string]any
}

//go:generate counterfeiter -o mock/ts.go -fake-name TransferService . TransferService

// TransferService defines the methods to manage the token transfer lifecycle.
//
//go:generate counterfeiter -o mock/ts.go -fake-name TransferService . TransferService
type TransferService interface {
	// Transfer generates a TransferAction to transfer the specified tokens.
	// It uses the provided wallet and anchor, along with a list of token identifiers and target outputs.
	// Additional options can be provided through TransferOptions.
	// The method returns:
	// - A TransferAction, which encodes the transfer details for the ledger.
	// - TransferMetadata, containing additional information for the requester.
	// - An error if the transfer generation fails.
	Transfer(ctx context.Context, anchor TokenRequestAnchor, wallet OwnerWallet, ids []*token2.ID, outputs []*token2.Token, opts *TransferOptions) (TransferAction, *TransferMetadata, error)

	// VerifyTransfer validates the well-formedness and correctness of a TransferAction.
	// It ensures that the transfer adheres to the driver's rules and correctly spends its inputs
	// to produce its outputs.
	VerifyTransfer(ctx context.Context, tr TransferAction, outputMetadata []*TransferOutputMetadata) error

	// DeserializeTransferAction reconstructs a TransferAction from its serialized byte representation.
	DeserializeTransferAction(raw []byte) (TransferAction, error)
}
