/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
)

// TransferHTLCValidate validates the HTLC scripts in the transfer action.
// It ensures that HTLC scripts only transfer ownership of a single token and
// that the script conditions are met. It relies on TransferSignatureValidate
// having populated ctx.InputTokens and ctx.Signatures, and must run after it.
// Nil entries are guarded against explicitly so that it never panics.
func TransferHTLCValidate(_ context.Context, ctx *Context) error {
	now := time.Now()

	for i, in := range ctx.InputTokens {
		if in == nil {
			return errors.Errorf("nil input token at index [%d]", i)
		}
		owner, err := identity.UnmarshalTypedIdentity(in.Owner)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal owner of input token")
		}
		if owner.Type != htlc.ScriptType {
			continue
		}

		// HTLC script must be a 1-to-1 transfer of ownership
		if len(ctx.InputTokens) != 1 || len(ctx.TransferAction.Outputs) != 1 {
			return ErrInvalidHTLCAction
		}
		out := ctx.TransferAction.Outputs[0]

		// check that owner field in output is correct based on HTLC script (lock, claim, or reclaim)
		script, op, err := htlc2.VerifyOwner(in.Owner, out.Recipient, now)
		if err != nil {
			return errors.Wrap(err, "failed to verify transfer from htlc script")
		}

		if i >= len(ctx.Signatures) {
			return errors.Errorf("missing signature for input at index [%d]", i)
		}
		metadataKey, err := htlc2.MetadataClaimKeyCheck(ctx.TransferAction, script, op, ctx.Signatures[i])
		if err != nil {
			return errors.WithMessagef(err, "failed to check htlc metadata")
		}
		if op != htlc2.Reclaim {
			ctx.CountMetadataKey(metadataKey)
		}
	}

	for i := range ctx.TransferAction.Outputs {
		o := &ctx.TransferAction.Outputs[i]
		if o.IsRedeem() {
			continue
		}
		owner, err := identity.UnmarshalTypedIdentity(o.Recipient)
		if err != nil {
			return err
		}
		if owner.Type != htlc.ScriptType {
			continue
		}
		script := &htlc.Script{}
		if err := script.FromBytes(owner.Identity); err != nil {
			return err
		}
		if err := script.Validate(now); err != nil {
			return errors.WithMessagef(err, "htlc script invalid")
		}
		metadataKey, err := htlc2.MetadataLockKeyCheck(ctx.TransferAction, script)
		if err != nil {
			return errors.WithMessagef(err, "failed to check htlc metadata")
		}
		ctx.CountMetadataKey(metadataKey)
	}

	return nil
}
