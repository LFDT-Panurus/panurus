/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

// Driver is the interface that must be implemented by a token driver to create TokenManagerService instances.
//
//go:generate counterfeiter -o mock/driver.go -fake-name Driver . Driver
type Driver interface {
	PPReader
	// NewTokenService returns a new TokenManagerService instance.
	NewTokenService(tmsID TMSID, publicParams []byte) (TokenManagerService, error)
}

// ValidatorDriver is the interface that must be implemented by a token driver to create Validator instances.
//
//go:generate counterfeiter -o mock/validator_driver.go -fake-name ValidatorDriver . ValidatorDriver
type ValidatorDriver interface {
	PPReader
	// NewValidator returns a new Validator instance from the passed public parameters, enforcing
	// the passed resource limits on untrusted requests and actions.
	NewValidator(pp PublicParameters, limits ResourceLimits) (Validator, error)
}
