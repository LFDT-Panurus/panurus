/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"bytes"
	"context"
	"math"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

type AuditorService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	Deserializer            driver.Deserializer
	QueryEngine             driver.QueryEngine
	TracerProvider          trace.TracerProvider
}

func NewAuditorService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	deserializer driver.Deserializer,
	queryEngine driver.QueryEngine,
	tracerProvider trace.TracerProvider,
) *AuditorService {
	return &AuditorService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		Deserializer:            deserializer,
		QueryEngine:             queryEngine,
		TracerProvider:          tracerProvider,
	}
}

func (s *AuditorService) AuditorCheck(ctx context.Context, tokenRequest *driver.TokenRequest, tokenRequestMetadata *driver.TokenRequestMetadata, anchor driver.TokenRequestAnchor) error {
	tokenIDs, err := common.ExtractTokenIDsAndCheckDuplicates(tokenRequestMetadata, anchor)
	if err != nil {
		return err
	}

	auditTokens, err := common.RetrieveAuditTokens(ctx, s.Logger, s.QueryEngine, tokenIDs, anchor, common.DefaultAuditTokensNumRetries, common.DefaultAuditTokensRetryDelay)
	if err != nil {
		return err
	}

	if err := common.ValidateStructure(tokenRequest, tokenRequestMetadata, anchor); err != nil {
		return err
	}

	issueIndex := 0
	issues := tokenRequest.GetIssues()
	for i, action := range tokenRequestMetadata.Actions {
		if action.IssueMetadata != nil {
			err := common.ValidateIssueActionTokenTypes(action.IssueMetadata, auditTokens)
			if err != nil {
				return errors.Wrapf(err, "invalid issue action at index %d", i)
			}
			if issueIndex >= len(issues) {
				return errors.Errorf("missing issue action for metadata at index %d", i)
			}
			if err := s.checkUpgradeIssue(issues[issueIndex], action.IssueMetadata, auditTokens); err != nil {
				return errors.Wrapf(err, "invalid upgrade issue action at index %d", i)
			}
			issueIndex++
		} else if action.TransferMetadata != nil {
			err := common.ValidateTransferActionTokenTypes(action.TransferMetadata, auditTokens, false, 0)
			if err != nil {
				return errors.Wrapf(err, "invalid transfer action at index %d", i)
			}
		}
	}

	return nil
}

// checkUpgradeIssue checks, for an upgrade issuance, that the metadata
// describes the action exactly: the input token IDs match in order, every
// parsed input matches the auditor's own copy of that token (owner, type and
// quantity), and the opened output notes carry the redeemed type and total
// value. A plain issuance is left to the generic checks.
func (s *AuditorService) checkUpgradeIssue(raw []byte, md *driver.IssueMetadata, auditTokens map[string]*token.Token) error {
	action := &snarktoken.IssueAction{}
	if err := action.Deserialize(raw); err != nil {
		return errors.Wrap(err, "failed to deserialize issue action")
	}
	if len(action.Inputs) == 0 {
		return nil
	}
	if err := action.Validate(); err != nil {
		return err
	}
	p := s.PublicParametersManager.PublicParams()
	summary, err := snarktoken.SummarizeUpgradeInputs(action.Inputs, p.PrecisionValue)
	if err != nil {
		return err
	}

	if len(md.Inputs) != len(action.Inputs) {
		return errors.Errorf("expected [%d] input metadata entries, got [%d]", len(action.Inputs), len(md.Inputs))
	}
	for i, in := range action.Inputs {
		if md.Inputs[i] == nil || md.Inputs[i].TokenID == nil || !md.Inputs[i].TokenID.Equal(in.ID) {
			return errors.Errorf("input [%d]: metadata token ID does not match [%s]", i, in.ID)
		}
		if err := checkUpgradeInputAgainstAuditToken(i, in, auditTokens[in.ID.String()], p.PrecisionValue); err != nil {
			return err
		}
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
		if token.Type(note.TokenType) != summary.Type {
			return errors.Errorf("output [%d]: note type [%s] differs from redeemed type [%s]", i, note.TokenType, summary.Type)
		}
		if note.Value > math.MaxUint64-total {
			return errors.Errorf("output [%d]: total value overflows", i)
		}
		total += note.Value
	}
	if total != summary.Total {
		return errors.Errorf("minted value [%d] differs from redeemed value [%d]", total, summary.Total)
	}

	return nil
}

// checkUpgradeInputAgainstAuditToken checks that the i-th redeemed input, as
// carried by the action, has the owner, type and quantity of the auditor's own
// copy of the token. Quantities are compared numerically at precision, so
// that, for example, "0x10" and "16" match.
func checkUpgradeInputAgainstAuditToken(i int, in *snarktoken.IssueActionInput, audited *token.Token, precision uint64) error {
	if audited == nil {
		return errors.Errorf("input [%d]: token [%s] not found among the audit tokens", i, in.ID)
	}
	parsed, err := snarktoken.ParseFabtokenOutput(in.Token, precision)
	if err != nil {
		return errors.WithMessagef(err, "input [%d]", i)
	}
	if !parsed.Owner.Equal(audited.Owner) {
		return errors.Errorf("input [%d]: owner differs from the audited token [%s]", i, in.ID)
	}
	if parsed.Type != audited.Type {
		return errors.Errorf("input [%d]: type [%s] differs from the audited type [%s]", i, parsed.Type, audited.Type)
	}
	q, err := token.NewUBigQuantity(audited.Quantity, precision)
	if err != nil {
		return errors.Wrapf(err, "input [%d]: invalid audited quantity [%s]", i, audited.Quantity)
	}
	if !q.IsUint64() || q.Uint64() != parsed.Value {
		return errors.Errorf("input [%d]: quantity [%d] differs from the audited quantity [%s]", i, parsed.Value, audited.Quantity)
	}

	return nil
}
