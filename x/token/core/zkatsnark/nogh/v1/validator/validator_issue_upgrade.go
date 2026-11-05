/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrUpgradeValueMismatch is returned when the value minted by an upgrade
	// issuance differs from the total value of the redeemed fabtoken outputs.
	ErrUpgradeValueMismatch = errors.New("validator: upgrade issue value does not match redeemed inputs")
)

// IssueUpgradeInputsValidate is a ValidateIssueFunc callback that binds an
// upgrade issuance (burn-and-re-issue of legacy fabtoken outputs) to the tokens
// it redeems. It is a no-op for a plain issuance. An upgrade issuance of
// ZKAT-DLOG outputs is checked by validateDLogUpgradeInputs instead: inputs
// well-formed and of the configured ZKAT-DLOG generation, and TypeCommitment
// opening to TokenType; its values are bound by the migration proofs and the
// binding signature verified in IssueZKValidate.
//
// It must run after IssueZKValidate (outputs are well-formed and TotalValue is
// the value they commit to, by the binding signature). It checks that:
//   - every input is a well-formed fabtoken output with a plain owner, the
//     inputs are pairwise distinct and of a single type (SummarizeUpgradeInputs);
//   - that type is IssueAction.TokenType, and TypeCommitment opens to it with
//     the revealed TypeRandomness, so the minted notes carry the redeemed type;
//   - the redeemed total equals TotalValue, so the minted value is the
//     redeemed value.
//
// As in zkatdlog, the owners' signatures are not required on the ledger: the
// issuer signature (IssueSignatureValidate) authorizes the burn, and the
// issuer verifies the owners' consent off-chain, through the upgrade proof,
// before assembling the issuance. Existence and single use of the inputs are
// enforced by the ledger translator, which checks and deletes the key derived
// from each input's serialized bytes (GetSerializedInputs).
func (v *Validator) IssueUpgradeInputsValidate(_ context.Context, ctx *Context) error {
	return v.validateUpgradeInputs(ctx.IssueAction)
}

// validateUpgradeInputs runs the upgrade checks of IssueUpgradeInputsValidate
// on a, without a validation context. Validator.ValidateIssue also calls it, so
// that the local IssueService.VerifyIssue path checks upgrade issuances too.
func (v *Validator) validateUpgradeInputs(a *snarktoken.IssueAction) error {
	if a == nil {
		return errors.Wrap(ErrMalformedAction, "nil issue action")
	}
	if len(a.Inputs) == 0 {
		if len(a.TypeRandomness) != 0 {
			return errors.Wrap(ErrMalformedAction, "type randomness revealed by an issue without inputs")
		}

		return nil
	}

	// a single kind of input, and a migration proof exactly for ZKAT-DLOG
	// inputs; completeness and duplicates are checked by the summaries below
	if err := a.CheckUpgradeInputKinds(); err != nil {
		return errors.WithMessagef(errors.Join(ErrMalformedAction, err), "%s", err.Error())
	}
	if a.IsDLogUpgrade() {
		return v.validateDLogUpgradeInputs(a)
	}

	summary, err := snarktoken.SummarizeUpgradeInputs(a.Inputs, v.pp.PrecisionValue)
	if err != nil {
		// Join keeps both sentinels reachable with errors.Is, but prints only
		// the first: carry the cause in the message too
		return errors.WithMessagef(errors.Join(ErrMalformedAction, err), "%s", err.Error())
	}

	if string(summary.Type) != a.TokenType {
		return errors.Wrapf(ErrTypeMismatch, "redeemed type [%s] differs from issued type [%s]", summary.Type, a.TokenType)
	}
	if err := v.checkTypeOpening(a); err != nil {
		return err
	}

	total, err := parseTotalValue(v.pp.Curve, a.TotalValue)
	if err != nil {
		return err
	}
	if total != summary.Total {
		return errors.Wrapf(ErrUpgradeValueMismatch, "issued [%d], redeemed [%d]", total, summary.Total)
	}

	return nil
}
