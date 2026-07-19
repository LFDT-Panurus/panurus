/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"os"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Environment variables read by EnvResourceLimitsProvider. Each is optional; an unset variable
// leaves the corresponding driver.ResourceLimits field at zero, which WithDefaults then replaces
// with driver.DefaultResourceLimits().
const (
	EnvMaxRequestBytes   = "TOKEN_VALIDATION_MAX_REQUEST_BYTES"
	EnvMaxActions        = "TOKEN_VALIDATION_MAX_ACTIONS"
	EnvMaxSignatures     = "TOKEN_VALIDATION_MAX_SIGNATURES"
	EnvMaxSignatureBytes = "TOKEN_VALIDATION_MAX_SIGNATURE_BYTES"
	EnvMaxActionBytes    = "TOKEN_VALIDATION_MAX_ACTION_BYTES"

	EnvMaxInputs             = "TOKEN_VALIDATION_MAX_INPUTS"
	EnvMaxOutputs            = "TOKEN_VALIDATION_MAX_OUTPUTS"
	EnvMaxMetadataEntries    = "TOKEN_VALIDATION_MAX_METADATA_ENTRIES"
	EnvMaxMetadataKeyBytes   = "TOKEN_VALIDATION_MAX_METADATA_KEY_BYTES"
	EnvMaxMetadataValueBytes = "TOKEN_VALIDATION_MAX_METADATA_VALUE_BYTES"

	EnvMaxProofBytes = "TOKEN_VALIDATION_MAX_PROOF_BYTES"
)

// EnvResourceLimitsProvider resolves driver.ResourceLimits from environment variables, overlaying
// driver.DefaultResourceLimits() onto any variable that is unset. It is the implementation used
// by the standalone Fabric chaincode process (cmd/main.go), which has no config service wired.
type EnvResourceLimitsProvider struct {
	// Getenv defaults to os.Getenv; overridable for tests.
	Getenv func(key string) string
}

// NewEnvResourceLimitsProvider returns a driver.ResourceLimitsProvider backed by environment
// variables.
func NewEnvResourceLimitsProvider() *EnvResourceLimitsProvider {
	return &EnvResourceLimitsProvider{Getenv: os.Getenv}
}

// ResourceLimits implements driver.ResourceLimitsProvider.
func (p *EnvResourceLimitsProvider) ResourceLimits() (driver.ResourceLimits, error) {
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	var l driver.ResourceLimits
	fields := []struct {
		env string
		dst *int
	}{
		{EnvMaxRequestBytes, &l.MaxRequestBytes},
		{EnvMaxActions, &l.MaxActions},
		{EnvMaxSignatures, &l.MaxSignatures},
		{EnvMaxSignatureBytes, &l.MaxSignatureBytes},
		{EnvMaxActionBytes, &l.MaxActionBytes},
		{EnvMaxInputs, &l.MaxInputs},
		{EnvMaxOutputs, &l.MaxOutputs},
		{EnvMaxMetadataEntries, &l.MaxMetadataEntries},
		{EnvMaxMetadataKeyBytes, &l.MaxMetadataKeyBytes},
		{EnvMaxMetadataValueBytes, &l.MaxMetadataValueBytes},
		{EnvMaxProofBytes, &l.MaxProofBytes},
	}
	for _, f := range fields {
		raw := getenv(f.env)
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return driver.ResourceLimits{}, errors.Wrapf(err, "invalid value [%s] for environment variable [%s]", raw, f.env)
		}
		*f.dst = v
	}

	return l.WithDefaults(), nil
}
