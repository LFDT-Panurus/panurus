/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
)

// TokenUpgradeRequest is a request to convert tokens
type TokenUpgradeRequest struct {
	// Challenge is a challenge to be solved by the prover
	Challenge TokensUpgradeChallenge
	// Tokens is a list of tokens to be converted
	Tokens []token.LedgerToken
	// Proof is a proof that the prover has solved the challenge
	Proof TokensUpgradeProof
}

// IssueOptions models the options that can be passed to the issue command
type IssueOptions struct {
	// Attributes is a container of generic options that might be driver specific
	Attributes map[string]any
	// TokensUpgradeRequest is a request to upgrade tokens
	TokensUpgradeRequest *TokenUpgradeRequest
	// Wallet is the wallet that should be used to issue the tokens.
	Wallet IssuerWallet
}

// IssueService defines the methods to manage the token issuance lifecycle.
//
//go:generate counterfeiter -o mock/issue_service.go -fake-name IssueService . IssueService
type IssueService interface {
	// Issue generates an IssuerAction for the issuance of tokens of the specified type.
	// It takes the identity of the issuer, the token type, and a list of values and their respective owners.
	// Optional configuration can be provided through IssueOptions.
	// The method returns:
	// - An IssueAction, which captures the issuance details for the ledger.
	// - IssueMetadata, containing additional information about the issuance for the requester.
	// - An error if the issuance generation fails.
	Issue(ctx context.Context, issuerIdentity Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *IssueOptions) (IssueAction, *IssueMetadata, error)

	// VerifyIssue validates the well-formedness and correctness of an IssueAction.
	// It checks the action against the provided metadata to ensure that the issuance is valid
	// according to the driver's rules.
	VerifyIssue(ctx context.Context, ia IssueAction, metadata []*IssueOutputMetadata) error

	// DeserializeIssueAction reconstructs an IssueAction from its serialized byte representation.
	DeserializeIssueAction(raw []byte) (IssueAction, error)
}
