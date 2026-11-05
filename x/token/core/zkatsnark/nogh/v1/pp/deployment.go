/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ValidateForDeployment checks configuration choices that are technically valid
// but may indicate a misconfiguration. It does not change the behaviour of
// Validate(); empty IssuerIdentities remain permitted (open-policy mode).
//
// Returns a non-nil error if the public parameters appear to be in open-policy
// mode (IssuerIdentities is empty), so that callers can surface an explicit
// warning at deployment time instead of silently operating in a mode where any
// identity can issue or redeem tokens. It mirrors the zkatdlog driver's
// PublicParams.ValidateForDeployment.
func (pp *PublicParams) ValidateForDeployment() error {
	if len(pp.IssuerIdentities) == 0 {
		return errors.New("open-policy mode: IssuerIdentities is empty — any identity can issue and redeem tokens; populate IssuerIdentities to restrict minting and redemption")
	}

	return nil
}
