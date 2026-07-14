/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deserializer

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TypedAuditInfoMatcher struct {
	matcher driver.Matcher
}

func (t *TypedAuditInfoMatcher) Match(ctx context.Context, id []byte) error {
	// match identity and audit info
	recipient, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal identity [%s]", id)
	}
	err = t.matcher.Match(ctx, recipient.Identity)
	if err != nil {
		return errors.Wrapf(err, "failed to match identity [%s] to audit infor", id)
	}

	return nil
}
