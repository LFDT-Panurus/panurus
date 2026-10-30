/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"bytes"
	"math"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// checkDLogUpgradeIssue checks, for an upgrade issuance of ZKAT-DLOG outputs,
// that the metadata describes the action exactly: the input token IDs match in
// order, every redeemed ledger token has the owner of the auditor's own
// (opened) copy of that token, whose type is the issued type, and the opened
// output notes carry that type and sum to the audited input quantities. The
// ledger validator enforces the same conservation cryptographically; this
// check lets the auditor refuse a request whose metadata misrepresents it.
func (s *AuditorService) checkDLogUpgradeIssue(action *snarktoken.IssueAction, md *driver.IssueMetadata, auditTokens map[string]*token.Token) error {
	p := s.PublicParametersManager.PublicParams()
	legacy, err := p.LegacyDLog()
	if err != nil {
		return errors.WithMessagef(err, "ZKAT-DLOG upgrades are not configured")
	}
	parsed, err := snarktoken.SummarizeDLogInputs(action.Inputs, legacy.PP)
	if err != nil {
		return err
	}
	if len(md.Inputs) != len(action.Inputs) {
		return errors.Errorf("expected [%d] input metadata entries, got [%d]", len(action.Inputs), len(md.Inputs))
	}
	var redeemed uint64
	for i, in := range action.Inputs {
		if md.Inputs[i] == nil || md.Inputs[i].TokenID == nil || !md.Inputs[i].TokenID.Equal(in.ID) {
			return errors.Errorf("input [%d]: metadata token ID does not match [%s]", i, in.ID)
		}
		audited := auditTokens[in.ID.String()]
		if audited == nil {
			return errors.Errorf("input [%d]: token [%s] not found among the audit tokens", i, in.ID)
		}
		if !parsed[i].Owner.Equal(audited.Owner) {
			return errors.Errorf("input [%d]: owner differs from the audited token [%s]", i, in.ID)
		}
		if string(audited.Type) != action.TokenType {
			return errors.Errorf("input [%d]: audited type [%s] differs from the issued type [%s]", i, audited.Type, action.TokenType)
		}
		q, err := token.NewUBigQuantity(audited.Quantity, p.PrecisionValue)
		if err != nil {
			return errors.Wrapf(err, "input [%d]: invalid audited quantity [%s]", i, audited.Quantity)
		}
		if q.Sign() <= 0 || !q.IsUint64() || q.Uint64() > math.MaxUint64-redeemed {
			return errors.Errorf("input [%d]: audited quantity [%s] out of range", i, audited.Quantity)
		}
		redeemed += q.Uint64()
	}

	if len(md.Outputs) != len(action.Outputs) {
		return errors.Errorf("expected [%d] output metadata entries, got [%d]", len(action.Outputs), len(md.Outputs))
	}
	var total uint64
	for i, out := range action.Outputs {
		if md.Outputs[i] == nil {
			return errors.Errorf("output [%d]: missing metadata", i)
		}
		note, err := snarktoken.Deserialize(md.Outputs[i].OutputMetadata)
		if err != nil {
			return errors.Wrapf(err, "output [%d]: failed to deserialize note", i)
		}
		cm, err := note.Commitment(p.Curve)
		if err != nil {
			return errors.Wrapf(err, "output [%d]: failed to compute commitment", i)
		}
		if !bytes.Equal(cm, out.CommitmentOut) {
			return errors.Errorf("output [%d]: note does not open the output commitment", i)
		}
		if note.TokenType != action.TokenType {
			return errors.Errorf("output [%d]: note type [%s] differs from the issued type [%s]", i, note.TokenType, action.TokenType)
		}
		if note.Value > math.MaxUint64-total {
			return errors.Errorf("output [%d]: total value overflows", i)
		}
		total += note.Value
	}
	if total != redeemed {
		return errors.Errorf("minted value [%d] differs from the audited redeemed value [%d]", total, redeemed)
	}

	return nil
}
