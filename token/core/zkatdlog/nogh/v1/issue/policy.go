/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrOpenPolicyIssueWithInputs is returned when an issue action carries inputs under an open issuer policy
var ErrOpenPolicyIssueWithInputs = errors.New("issue actions with inputs are not allowed under an open issuer policy")

// CheckInputsPolicy rejects an issue action carrying numInputs inputs when issuers is empty
// (open issuer policy). Issue action inputs carry no owner authorization: every issuer in
// PP.IssuerIDs is trusted to consume any output in the namespace. Under an open policy that
// set is anyone, so issue actions with inputs are rejected (GHSA-8pg9-2ph5-j563).
// Both the validator and the auditor call this so that they enforce the same rule.
func CheckInputsPolicy(issuers []driver.Identity, numInputs int) error {
	if numInputs > 0 && len(issuers) == 0 {
		return ErrOpenPolicyIssueWithInputs
	}

	return nil
}
