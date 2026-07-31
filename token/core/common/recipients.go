/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// AuditableRecipients expands an output owner into the receivers to record in
// the action metadata, one entry per recipient with its own audit info.
//
// A plain identity expands to itself, so this is a no-op for the common case.
// A composite owner (a policy or multisig identity) expands to its members:
// the auditing side rebuilds recipients from the ledger output and matches
// them positionally against this list, so recording the composite owner as a
// single receiver makes every composite recipient fail that match.
func AuditableRecipients(
	ctx context.Context,
	deserializer driver.Deserializer,
	walletService driver.WalletService,
	owner driver.Identity,
) ([]*driver.AuditableIdentity, error) {
	recipients, err := deserializer.Recipients(owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting recipients of [%s]", owner)
	}
	receivers := make([]*driver.AuditableIdentity, 0, len(recipients))
	for _, recipient := range recipients {
		auditInfo, err := deserializer.GetAuditInfo(ctx, recipient, walletService)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting audit info for recipient [%s]", recipient)
		}
		receivers = append(receivers, &driver.AuditableIdentity{
			Identity:  recipient,
			AuditInfo: auditInfo,
		})
	}

	return receivers, nil
}
