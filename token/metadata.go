/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides token transaction metadata filtering and matching functionality for issues and transfers.
package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

// Metadata contains the metadata of a Token Request.
// This metadata is used to unscramble the content of the actions in the Token Request.
// It includes information about issuers, owners, and extra signers.
type Metadata struct {
	// TokenService is the service used to handle tokens.
	TokenService driver.TokensService
	// WalletService is the service used to handle wallets and identities.
	WalletService driver.WalletService
	// TokenRequestMetadata contains the metadata of the Token Request as defined by the driver.
	TokenRequestMetadata *driver.TokenRequestMetadata
	// Logger is the logger used by this struct.
	Logger logging.Logger
}

// SpentTokenID returns the token IDs of the tokens that were spent by the Token Request this metadata is associated with.
// It iterates over all issue and transfer actions and collects the IDs of the input tokens.
func (m *Metadata) SpentTokenID() []*token.ID {
	var res []*token.ID
	// Collect token IDs from all actions.
	for _, action := range m.TokenRequestMetadata.Actions {
		if action.IssueMetadata != nil {
			// Note: issue actions usually don't have inputs, but some drivers might use them (e.g., for token upgrades).
			for _, input := range action.IssueMetadata.Inputs {
				res = append(res, input.TokenID)
			}
		} else if action.TransferMetadata != nil {
			for _, input := range action.TransferMetadata.Inputs {
				res = append(res, input.TokenID)
			}
		}
	}

	return res
}

// FilterBy returns a new Metadata containing only the metadata that matches the given enrollment IDs.
// This is used to share with a party only the metadata they are entitled to see.
//
// For Issue actions:
// - The issuer information is always included.
// - Only metadata for outputs owned by the given enrollment IDs is included.
//
// For Transfer actions:
// - Only metadata for outputs owned by the given enrollment IDs is included.
// - Sender information is included if and only if there is at least one output owned by the given enrollment IDs.
//
// Application metadata is always included.
func (m *Metadata) FilterBy(ctx context.Context, eIDs ...string) (*Metadata, error) {
	if len(eIDs) == 0 {
		return m, nil
	}

	eIDSet := collections.NewSet(eIDs...)

	// Filter actions metadata
	filteredActions := make([]*driver.ActionMetadataEntry, 0, len(m.TokenRequestMetadata.Actions))
	issueCount := 0
	transferCount := 0

	for _, action := range m.TokenRequestMetadata.Actions {
		if action.IssueMetadata != nil {
			filtered, err := m.filterIssue(ctx, action.IssueMetadata, eIDSet)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed filtering issue")
			}
			filteredActions = append(filteredActions, &driver.ActionMetadataEntry{
				ActionID:      action.ActionID,
				IssueMetadata: filtered,
			})
			issueCount++
		} else if action.TransferMetadata != nil {
			filtered, err := m.filterTransfer(ctx, action.TransferMetadata, eIDSet)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed filtering transfer")
			}
			filteredActions = append(filteredActions, &driver.ActionMetadataEntry{
				ActionID:         action.ActionID,
				TransferMetadata: filtered,
			})
			transferCount++
		}
	}

	clone := &Metadata{
		TokenService:  m.TokenService,
		WalletService: m.WalletService,
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions:     filteredActions,
			Application: m.TokenRequestMetadata.Application,
		},
		Logger: m.Logger,
	}

	m.Logger.Debugf("filtered metadata for [% x] from [%d] actions to [%d] actions (%d issues, %d transfers)",
		eIDs,
		len(m.TokenRequestMetadata.Actions), len(filteredActions), issueCount, transferCount)

	return clone, nil
}

// filterIssue filters a single issue metadata based on the provided enrollment IDs.
func (m *Metadata) filterIssue(ctx context.Context, issue *driver.IssueMetadata, eIDSet collections.Set[string]) (*driver.IssueMetadata, error) {
	clone := &driver.IssueMetadata{
		Issuer:       issue.Issuer,
		Inputs:       issue.Inputs,
		Outputs:      nil,
		ExtraSigners: issue.ExtraSigners,
	}

	counter := 0
	for _, output := range issue.Outputs {
		// Check if any of the receivers of the output matches the enrollment IDs.
		if found, err := m.contains(ctx, output.Receivers, eIDSet); err != nil {
			return nil, errors.WithMessagef(err, "failed checking receivers")
		} else if found {
			// If matched, include the full output metadata.
			clone.Outputs = append(clone.Outputs, output)
			counter++
		} else {
			// If not matched, include a nil entry to preserve the indexing.
			clone.Outputs = append(clone.Outputs, nil)
		}
	}

	m.Logger.Debugf("keeping issue with [%d] out of [%d] outputs", counter, len(issue.Outputs))

	return clone, nil
}

// filterTransfer filters a single transfer metadata based on the provided enrollment IDs.
func (m *Metadata) filterTransfer(ctx context.Context, transfer *driver.TransferMetadata, eIDSet collections.Set[string]) (*driver.TransferMetadata, error) {
	clone := &driver.TransferMetadata{
		Inputs:       nil,
		Outputs:      nil,
		ExtraSigners: transfer.ExtraSigners,
	}

	// Filter outputs: if the receiver has the given enrollment ID, add it. Otherwise, add empty entries.
	counter := 0
	for _, output := range transfer.Outputs {
		if found, err := m.contains(ctx, output.Receivers, eIDSet); err != nil {
			return nil, errors.WithMessagef(err, "failed checking receivers")
		} else if found {
			clone.Outputs = append(clone.Outputs, output)
			counter++
		} else {
			clone.Outputs = append(clone.Outputs, nil)
		}
	}

	// Prepare empty input metadata entries.
	for range transfer.Inputs {
		clone.Inputs = append(clone.Inputs, &driver.TransferInputMetadata{})
	}
	// If at least one output matched, include the sender information for all inputs.
	if counter > 0 {
		for i, input := range transfer.Inputs {
			clone.Inputs[i].Senders = input.Senders
		}
	}

	m.Logger.Debugf("keeping transfer with [%d] out of [%d] outputs", counter, len(transfer.Outputs))

	return clone, nil
}

// contains checks if any of the given auditable identities matches the provided enrollment IDs.
func (m *Metadata) contains(ctx context.Context, receivers []*driver.AuditableIdentity, eIDSet collections.Set[string]) (bool, error) {
	for _, receiver := range receivers {
		// Resolve the enrollment ID of the receiver.
		recipientEID, err := m.WalletService.GetEnrollmentID(ctx, receiver.Identity, receiver.AuditInfo)
		if err != nil {
			return false, errors.Wrap(err, "failed getting enrollment ID")
		}
		// Check if the enrollment ID is in the set.
		if eIDSet.Contains(recipientEID) {
			logger.Debugf("eid [%s] found in list [%v]", recipientEID, eIDSet)

			return true, nil
		} else {
			logger.Debugf("eid [%s] not found in list [%v]", recipientEID, eIDSet)
		}
	}

	return false, nil
}

// Issue returns the i-th issue metadata, if present.
func (m *Metadata) Issue(i int) (*IssueMetadata, error) {
	issueMeta, err := m.TokenRequestMetadata.GetIssueMetadata(i)
	if err != nil {
		return nil, err
	}

	return &IssueMetadata{IssueMetadata: issueMeta}, nil
}

// Transfer returns the i-th transfer metadata, if present.
func (m *Metadata) Transfer(i int) (*TransferMetadata, error) {
	transferMeta, err := m.TokenRequestMetadata.GetTransferMetadata(i)
	if err != nil {
		return nil, err
	}

	return &TransferMetadata{TransferMetadata: transferMeta}, nil
}

// IssueMetadata contains the metadata of an issue action.
type IssueMetadata struct {
	*driver.IssueMetadata
}

// Match returns true if the given action matches this metadata.
// It performs a deep check of inputs, outputs, extra signers, and the issuer identity.
func (m *IssueMetadata) Match(action *IssueAction) error {
	if action == nil {
		return errors.New("can't match issue metadata to issue action: nil issue action")
	}

	// Delegate to the driver's Match method
	return m.IssueMetadata.Match(action.a)
}

// IsOutputAbsent returns true if the j-th output's metadata is absent (e.g., filtered out).
func (m *IssueMetadata) IsOutputAbsent(j int) bool {
	if j < 0 || j >= len(m.Outputs) {
		return true
	}

	return m.Outputs[j] == nil
}

// TransferMetadata contains the metadata of a transfer action.
type TransferMetadata struct {
	*driver.TransferMetadata
}

// Match returns true if the given action matches this metadata.
// It performs a deep check of inputs, outputs, extra signers, and the issuer identity (if present).
func (m *TransferMetadata) Match(action *TransferAction) error {
	if action == nil {
		return errors.New("can't match transfer metadata to transfer action: nil transfer action")
	}

	// Delegate to the driver's Match method
	return m.TransferMetadata.Match(action.TransferAction)
}

// IsOutputAbsent returns true if the j-th output's metadata is absent (e.g., filtered out).
func (m *TransferMetadata) IsOutputAbsent(j int) bool {
	if j < 0 || j >= len(m.Outputs) {
		return true
	}

	return m.Outputs[j] == nil
}

// IsInputAbsent returns true if the j-th input's metadata is absent (e.g., filtered out).
func (m *TransferMetadata) IsInputAbsent(j int) bool {
	if j < 0 || j >= len(m.Inputs) {
		return true
	}

	return m.Inputs[j] == nil || len(m.Inputs[j].Senders) == 0
}
