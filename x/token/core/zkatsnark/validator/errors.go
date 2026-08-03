/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package validator implements the stateless, network-agnostic validity
// check for zkatsnark actions.
//
// The validator takes an Action and a *pp.PublicParams and returns nil
// (valid) or an error (invalid). It never reads ledger state: token
// existence, double-spend prevention, and ownership are backend-network
// responsibilities (MVCC in Fabric's case), not this package's. Every
// check here is either purely cryptographic (proof verification, binding
// signature verification) or a static comparison against data already
// loaded into PublicParams (issuer authorization), nothing here performs
// I/O of any kind.
package validator

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

var (
	// ErrMalformedAction is returned for any structural violation: wrong
	// byte lengths, missing required fields. Checked first, before any
	// cryptographic work, since it's the cheapest possible rejection.
	ErrMalformedAction = errors.New("validator: action is malformed")

	// ErrInvalidEncoding is returned when a byte field does not decode to
	// a canonical field element, or a decoded point is not on the Jubjub
	// curve.
	ErrInvalidEncoding = errors.New("validator: field encoding invalid")

	// ErrTypeMismatch is returned when an input or output's TokenType does
	// not match the action's declared TokenType.
	ErrTypeMismatch = errors.New("validator: token type mismatch across action")

	// ErrInvalidProof is returned when a SpendProof or OutputProof fails
	// groth16.Verify against the corresponding circuit's verification key.
	ErrInvalidProof = errors.New("validator: proof verification failed")

	// ErrInvalidBindingSignature is returned when an action's binding
	// signature does not verify against the independently-recomputed bvk.
	ErrInvalidBindingSignature = errors.New("validator: binding signature invalid")

	// ErrUnauthorizedIssuer is returned when an IssueAction's issuer is
	// not present in PublicParams.Issuers().
	ErrUnauthorizedIssuer = errors.New("validator: issuer not authorized")

	// ErrMigrationNotConfigured is returned by ValidateMigration when this
	// deployment's PublicParams has no migration verification key loaded.
	ErrMigrationNotConfigured = errors.New("validator: migration circuit not configured for this deployment")
)
