/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrInvalidInputs is returned when the number of token inputs is invalid
	ErrInvalidInputs = errors.New("invalid number of token inputs, expected at least 1")
	// ErrMissingIssuer is returned when an issuer is missing on a redeem action
	ErrMissingIssuer = errors.New("On Redeem action, must have at least one issuer")
	// ErrFabTokenNotFound is returned when the fabtoken token is not found in the witness
	ErrFabTokenNotFound = errors.New("fabtoken token not found in witness")
	// ErrCommitmentMismatch is returned when the recomputed commitment does not match
	ErrCommitmentMismatch = errors.New("recomputed commitment does not match")
	// ErrOwnersMismatch is returned when the owners do not correspond
	ErrOwnersMismatch = errors.New("owners do not correspond")
	// ErrInvalidHTLCAction is returned when an HTLC action is invalid
	ErrInvalidHTLCAction = errors.New("invalid transfer action: an htlc script only transfers the ownership of a token")
	// ErrHTLCOutputNotFound is returned when the HTLC output is not found
	ErrHTLCOutputNotFound = errors.New("invalid transfer action: an htlc script only transfers the ownership of a token, output not found")
	// ErrIssueVerificationFailed is returned when issue verification fails
	ErrIssueVerificationFailed = errors.New("failed to verify issue")
	// ErrIssuerNotAuthorized is returned when the issuer is not authorized
	ErrIssuerNotAuthorized = errors.New("issuer is not authorized")
	// ErrOpenPolicyIssueWithInputs is returned when an issue action carries inputs under an open issuer policy
	ErrOpenPolicyIssueWithInputs = issue.ErrOpenPolicyIssueWithInputs
	// ErrInvalidZKP  is returned when the zk proof is not valid
	ErrInvalidZKP = errors.New("invalid zero-knowledge proof")
)
