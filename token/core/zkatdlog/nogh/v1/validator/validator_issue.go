/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// IssueValidate validates the issue action by checking its structure, verifying the zero-knowledge proof,
// and ensuring the issuer is authorized and has signed the action.
//
// Open-policy issuer behaviour: when PP.IssuerIDs is empty (len(ctx.PP.Issuers()) == 0),
// the issuer authorization check is skipped and any identity may issue tokens.
// This is an intentional open-policy design for deployments that do not restrict issuance
// to a fixed set of identities. When issuer restriction is required, populate PP.IssuerIDs
// with the authorized issuer identities before deploying the public parameters.
func IssueValidate(c context.Context, ctx *Context) error {
	action := ctx.IssueAction

	if err := action.Validate(); err != nil {
		return errors.Wrapf(err, "failed validating issue action")
	}

	commitments, err := action.GetCommitments()
	if err != nil {
		return ErrIssueVerificationFailed
	}
	// Verify the zero-knowledge proof that the commitments are well-formed
	zkVerifier, err := issue.NewVerifier(commitments, ctx.PP, action.ProofType)
	if err != nil {
		return errors.Join(err, ErrInvalidZKP)
	}
	if err := zkVerifier.Verify(action.GetProof()); err != nil {
		return errors.Join(err, ErrInvalidZKP)
	}

	// Check the issuer is among those known
	if issuers := ctx.PP.Issuers(); len(issuers) != 0 && !slices.ContainsFunc(issuers, action.Issuer.Equal) {
		return ErrIssuerNotAuthorized
	}
	logger.Debugf("Found issue owner [%s]", action.Issuer)

	verifier, err := ctx.Deserializer.GetIssuerVerifier(c, action.Issuer)
	if err != nil {
		return errors.Wrapf(err, "failed getting verifier for issuer [%s]", action.Issuer.String())
	}
	if _, err := ctx.SignatureProvider.HasBeenSignedBy(c, action.Issuer, verifier); err != nil {
		return errors.Wrapf(err, "failed verifying signature")
	}

	return nil
}
