/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Authorizer decides whether an FSC identity may request endorsement. It is the EVM analog of the
// Fabric responder's MSP/ACL creator check: EVM has no MSP, so authorization is membership in an
// allowlist of FSC identities configured per TMS (design §6.2, §15.7). The default policy (the TMS
// network's nodes) is resolved at config load in Week 5; here the allowlist is supplied explicitly.
//
// It is fail-closed: an empty allowlist is rejected at construction, and an unknown or empty caller
// is denied, so a misconfiguration cannot silently authorize everyone.
type Authorizer struct {
	allow map[string]struct{}
}

// NewAuthorizer builds an Authorizer from the allowlist of FSC identities permitted to request
// endorsement. It rejects an empty allowlist (fail-closed) and any empty identity within it.
func NewAuthorizer(allowlist []view.Identity) (*Authorizer, error) {
	if len(allowlist) == 0 {
		return nil, errors.New("authorizer: empty allowlist")
	}
	allow := make(map[string]struct{}, len(allowlist))
	for i, id := range allowlist {
		if id.IsNone() {
			return nil, errors.Errorf("authorizer: allowlist entry %d is empty", i)
		}
		allow[id.UniqueID()] = struct{}{}
	}

	return &Authorizer{allow: allow}, nil
}

// Authorize returns nil if caller is in the allowlist, ErrUnauthorized otherwise. caller is the
// identity the FSC session authenticated, not a self-declared value, so this is a genuine
// authorization check.
func (a *Authorizer) Authorize(caller view.Identity) error {
	if caller.IsNone() {
		return errors.Wrap(ErrUnauthorized, "empty caller identity")
	}
	if _, ok := a.allow[caller.UniqueID()]; !ok {
		return errors.Wrapf(ErrUnauthorized, "identity [%s]", caller)
	}

	return nil
}
