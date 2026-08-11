/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLedgerOutput is a minimal driver.Output for extract*Outputs tests.
type stubLedgerOutput struct {
	raw []byte
}

func (s *stubLedgerOutput) Serialize() ([]byte, error) { return s.raw, nil }
func (s *stubLedgerOutput) IsRedeem() bool             { return false }
func (s *stubLedgerOutput) GetOwner() []byte           { return []byte("policy-owner") }

// compositeRecipients returns the two member identities and the output
// metadata carrying them as receivers of a single ledger output.
func compositeRecipients() ([]Identity, []*driver.AuditableIdentity) {
	m0 := Identity("member-0")
	m1 := Identity("member-1")
	receivers := []*driver.AuditableIdentity{
		{Identity: m0, AuditInfo: []byte("audit-info-0")},
		{Identity: m1, AuditInfo: []byte("audit-info-1")},
	}

	return []Identity{m0, m1}, receivers
}

// newCompositeTransferRequest builds a Request holding one transfer action
// with a single 40-unit output owned by a composite identity whose two
// members are enumerated as recipients.
func newCompositeTransferRequest(t *testing.T, ws *driver2.WalletService) *Request {
	t.Helper()
	recipients, receivers := compositeRecipients()

	transferAction := &driver2.TransferAction{}
	transferAction.NumInputsReturns(0)
	transferAction.NumOutputsReturns(1)
	transferAction.GetOutputsReturns([]driver.Output{&stubLedgerOutput{raw: []byte("ledger-output")}})

	transferService := &driver2.TransferService{}
	transferService.DeserializeTransferActionReturns(transferAction, nil)

	tokensService := &driver2.TokensService{}
	tokensService.DeobfuscateReturns(
		&token.Token{Owner: Identity("policy-owner"), Type: "USD", Quantity: "0x28"},
		nil, recipients, token.Format("fabtoken"), nil)

	pp := &driver2.PublicParameters{}
	pp.PrecisionReturns(64)
	ppm := &driver2.PublicParamsManager{}
	ppm.PublicParametersReturns(pp)

	tms := &driver2.TokenManagerService{}
	tms.TransferServiceReturns(transferService)
	tms.IssueServiceReturns(&driver2.IssueService{})
	tms.TokensServiceReturns(tokensService)
	tms.WalletServiceReturns(ws)
	tms.PublicParamsManagerReturns(ppm)

	return &Request{
		Anchor: "composite-anchor",
		Actions: &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
			},
		},
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					TransferMetadata: &driver.TransferMetadata{
						Outputs: []*driver.TransferOutputMetadata{
							{
								OutputMetadata:  []byte("output-metadata"),
								OutputAuditInfo: []byte("output-audit-info"),
								Receivers:       receivers,
							},
						},
					},
				},
			},
		},
		TokenService: &ManagementService{
			tms:    tms,
			logger: logging.MustGetLogger(),
		},
	}
}

// TestRequest_Outputs_CompositeSameEIDKeepsMemberRows checks that every
// member of a composite owner keeps its own output row (identity and
// revocation handle stay visible), while UniquePerOutput collapses the rows
// so eid-keyed sums count the output amount exactly once.
func TestRequest_Outputs_CompositeSameEIDKeepsMemberRows(t *testing.T) {
	ws := &driver2.WalletService{}
	ws.GetEIDAndRHReturnsOnCall(0, "wallet-42", "rh-0", nil)
	ws.GetEIDAndRHReturnsOnCall(1, "wallet-42", "rh-1", nil)

	outputs, err := newCompositeTransferRequest(t, ws).Outputs(t.Context())
	require.NoError(t, err)

	// one row per member, same physical output index
	require.Equal(t, 2, outputs.Count())
	assert.Equal(t, "wallet-42", outputs.At(0).EnrollmentID)
	assert.Equal(t, "wallet-42", outputs.At(1).EnrollmentID)
	assert.Equal(t, outputs.At(0).Index, outputs.At(1).Index)

	// identity consumers still see every member
	assert.Equal(t, 1, outputs.ByRecipient(Identity("member-0")).Count())
	assert.Equal(t, 1, outputs.ByRecipient(Identity("member-1")).Count())
	assert.Equal(t, []string{"rh-0", "rh-1"}, outputs.RevocationHandles())

	// economic view counts the amount once
	assert.Equal(t, "40", outputs.ByEnrollmentID("wallet-42").UniquePerOutput().Sum().String())
}

// TestRequest_Outputs_CompositeDistinctEIDsKeepRows checks that members
// spanning enrollments keep one row each (cross-enrollment visibility).
func TestRequest_Outputs_CompositeDistinctEIDsKeepRows(t *testing.T) {
	ws := &driver2.WalletService{}
	ws.GetEIDAndRHReturnsOnCall(0, "wallet-42", "", nil)
	ws.GetEIDAndRHReturnsOnCall(1, "wallet-43", "", nil)

	outputs, err := newCompositeTransferRequest(t, ws).Outputs(t.Context())
	require.NoError(t, err)

	require.Equal(t, 2, outputs.Count())
	assert.Equal(t, "wallet-42", outputs.At(0).EnrollmentID)
	assert.Equal(t, "wallet-43", outputs.At(1).EnrollmentID)
}

// newCompositeIssueRequest is the issue-action twin of
// newCompositeTransferRequest: one 40-unit issued output, two members.
func newCompositeIssueRequest(t *testing.T, ws *driver2.WalletService) *Request {
	t.Helper()
	recipients, receivers := compositeRecipients()
	issuer := Identity("issuer-1")

	issueAction := &driver2.IssueAction{}
	issueAction.NumInputsReturns(0)
	issueAction.NumOutputsReturns(1)
	issueAction.GetOutputsReturns([]driver.Output{&stubLedgerOutput{raw: []byte("ledger-output")}})
	issueAction.GetIssuerReturns(issuer)

	issueService := &driver2.IssueService{}
	issueService.DeserializeIssueActionReturns(issueAction, nil)

	tokensService := &driver2.TokensService{}
	tokensService.DeobfuscateReturns(
		&token.Token{Owner: Identity("policy-owner"), Type: "USD", Quantity: "0x28"},
		issuer, recipients, token.Format("fabtoken"), nil)

	pp := &driver2.PublicParameters{}
	pp.PrecisionReturns(64)
	ppm := &driver2.PublicParamsManager{}
	ppm.PublicParametersReturns(pp)

	tms := &driver2.TokenManagerService{}
	tms.IssueServiceReturns(issueService)
	tms.TransferServiceReturns(&driver2.TransferService{})
	tms.TokensServiceReturns(tokensService)
	tms.WalletServiceReturns(ws)
	tms.PublicParamsManagerReturns(ppm)

	return &Request{
		Anchor: "composite-issue-anchor",
		Actions: &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		},
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{Identity: issuer},
						Outputs: []*driver.IssueOutputMetadata{
							{
								OutputMetadata:  []byte("output-metadata"),
								OutputAuditInfo: []byte("output-audit-info"),
								Receivers:       receivers,
							},
						},
					},
				},
			},
		},
		TokenService: &ManagementService{
			tms:    tms,
			logger: logging.MustGetLogger(),
		},
	}
}

// TestRequest_Outputs_IssueCompositeSameEIDKeepsMemberRows is the issue-side
// twin of TestRequest_Outputs_CompositeSameEIDKeepsMemberRows.
func TestRequest_Outputs_IssueCompositeSameEIDKeepsMemberRows(t *testing.T) {
	ws := &driver2.WalletService{}
	ws.GetEIDAndRHReturnsOnCall(0, "wallet-42", "rh-0", nil)
	ws.GetEIDAndRHReturnsOnCall(1, "wallet-42", "rh-1", nil)

	outputs, err := newCompositeIssueRequest(t, ws).Outputs(t.Context())
	require.NoError(t, err)

	require.Equal(t, 2, outputs.Count())
	assert.Equal(t, []string{"rh-0", "rh-1"}, outputs.RevocationHandles())
	assert.Equal(t, "40", outputs.ByEnrollmentID("wallet-42").UniquePerOutput().Sum().String())
}
