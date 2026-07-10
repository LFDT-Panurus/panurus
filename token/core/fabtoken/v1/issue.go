/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type IssueService struct {
	PublicParamsManager driver.PublicParamsManager
	WalletService       driver.WalletService
	Deserializer        driver.Deserializer
}

func NewIssueService(publicParamsManager driver.PublicParamsManager, walletService driver.WalletService, deserializer driver.Deserializer) *IssueService {
	return &IssueService{PublicParamsManager: publicParamsManager, WalletService: walletService, Deserializer: deserializer}
}

// Issue returns an IssueAction as a function of the passed arguments
// Issue also returns a serialization OutputMetadata associated with issued tokens
// and the identity of the issuer
func (s *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token2.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	for _, owner := range owners {
		// a recipient cannot be empty
		if len(owner) == 0 {
			return nil, nil, errors.Errorf("all recipients should be defined")
		}
	}

	if issuerIdentity.IsNone() && len(tokenType) == 0 && values == nil {
		return nil, nil, errors.Errorf("issuer identity, token type and values should be defined")
	}
	if opts != nil {
		if opts.TokensUpgradeRequest != nil {
			return nil, nil, errors.Errorf("redeem during issue is not supported")
		}
	}

	precision := s.PublicParamsManager.PublicParameters().Precision()
	var outs []*v1.Output
	var outputsMetadata []*driver.IssueOutputMetadata
	for i, v := range values {
		q, err := token2.UInt64ToQuantity(v, precision)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to convert [%d] to quantity of precision [%d]", v, precision)
		}
		outs = append(outs, &v1.Output{
			Owner:    owners[i],
			Type:     tokenType,
			Quantity: q.Hex(),
		})

		outputMetadata := &v1.OutputMetadata{
			Issuer: issuerIdentity,
		}
		outputMetadataRaw, err := outputMetadata.Serialize()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed serializing token information")
		}
		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, owners[i], s.WalletService)
		if err != nil {
			return nil, nil, err
		}
		outputsMetadata = append(outputsMetadata, &driver.IssueOutputMetadata{
			OutputMetadata:  outputMetadataRaw,
			OutputAuditInfo: auditInfo,
			Receivers: []*driver.AuditableIdentity{
				{
					Identity:  owners[i],
					AuditInfo: auditInfo,
				},
			},
		})
	}
	issuerAuditInfo, err := s.Deserializer.GetAuditInfo(ctx, issuerIdentity, s.WalletService)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to get audit info for issuer identity")
	}

	action := &v1.IssueAction{
		Issuer:  issuerIdentity,
		Outputs: outs,
	}
	// add issuer action's metadata
	if opts != nil {
		action.Metadata = meta.IssueActionMetadata(opts.Attributes)
	}

	meta := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{
			Identity:  issuerIdentity,
			AuditInfo: issuerAuditInfo,
		},
		Inputs:       nil,
		Outputs:      outputsMetadata,
		ExtraSigners: nil,
	}

	return action, meta, nil
}

// VerifyIssue checks if the outputs of an IssueAction match the passed tokenInfos
func (s *IssueService) VerifyIssue(ctx context.Context, ia driver.IssueAction, metadata []*driver.IssueOutputMetadata) error {
	if ia == nil {
		return errors.Errorf("nil issue action")
	}
	action, ok := ia.(*v1.IssueAction)
	if !ok {
		return errors.Errorf("expected *actions.IssueAction, got [%T]", ia)
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(metadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(metadata))
	}

	precision := s.PublicParamsManager.PublicParameters().Precision()
	zero := token2.NewZeroQuantity(precision)
	for i, out := range action.Outputs {
		q, err := token2.ToQuantity(out.Quantity, precision)
		if err != nil {
			return errors.Wrapf(err, "failed parsing output quantity [%s]", out.Quantity)
		}
		if q.Cmp(zero) == 0 {
			return errors.Errorf("output [%d] has zero quantity", i)
		}

		// Metadata for an output can be legitimately absent here: a TokenRequest received
		// over the wire may have been filtered by enrollment ID before reaching us (see
		// Metadata.FilterBy), in which case outputs we are not a receiver of carry a nil
		// entry. Treat absent metadata as "nothing to verify from our vantage point" rather
		// than as a validation failure, mirroring the analogous check in VerifyTransfer.
		om := metadata[i] // #nosec G602 -- lengths already checked equal above
		if om == nil || len(om.OutputMetadata) == 0 {
			continue
		}
		outputMetadata := &v1.OutputMetadata{}
		if err := outputMetadata.Deserialize(om.OutputMetadata); err != nil {
			return errors.Wrapf(err, "failed unmarshalling metadata for output [%d]", i)
		}
		if !driver.Identity(outputMetadata.Issuer).Equal(action.Issuer) {
			return errors.Errorf("issuer in metadata for output [%d] does not match issuer in action", i)
		}

		if len(om.Receivers) == 0 {
			return errors.Errorf("missing receivers metadata for output [%d]", i)
		}
		for _, receiver := range om.Receivers {
			if err := s.Deserializer.MatchIdentity(ctx, receiver.Identity, receiver.AuditInfo); err != nil {
				return errors.Wrapf(err, "failed matching audit info for receiver of output [%d]", i)
			}
		}
	}

	return nil
}

// DeserializeIssueAction un-marshals the passed bytes into an IssueAction
// If unmarshalling fails, then DeserializeIssueAction returns an error
func (s *IssueService) DeserializeIssueAction(raw []byte) (driver.IssueAction, error) {
	issue := &v1.IssueAction{}
	if err := issue.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed deserializing issue action")
	}

	return issue, nil
}
