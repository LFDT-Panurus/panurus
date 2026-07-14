/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"os"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit/mock"
	zkatdlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	token3 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

// TestAuditor tests the Auditor's Check method for various scenarios, including successful audits
// of transfers and issues, and failure cases where audit information or token data is inconsistent.
func TestAuditor(t *testing.T) {
	// audit information is computed correctly tests a successful audit of a valid transfer request.
	t.Run("audit information is computed correctly", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}}, "1", auditTokens)
		require.NoError(t, err)
	})

	// token info does not match output tests that the audit fails when the token metadata (e.g., value)
	// does not match the commitment in the transfer output.
	t.Run("token info does not match output", func(t *testing.T) {
		fakeSigningIdentity, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransferWithBogusOutput(t, pp)
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}},
			&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}},
			"1", auditTokens,
		)
		require.Error(t, err)
		require.Equal(t, 0, fakeSigningIdentity.SignCallCount())
	})

	// sender audit info does not match input tests that the audit fails when the sender's audit information
	// does not match the input token's owner identity.
	t.Run("sender audit info does not match input", func(t *testing.T) {
		fakeSigningIdentity, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)
		// test idemix info
		_, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		metadata.Inputs[0].Senders[0].AuditInfo = auditInfoRaw
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}}, "1", auditTokens)
		require.Error(t, err)
		require.Contains(t, err.Error(), "owner at index [0] does not match the provided opening")
		require.NotContains(t, err.Error(), "attribute mistmatch")
		require.Equal(t, 0, fakeSigningIdentity.SignCallCount())
	})

	// recipient audit info does not match output tests that the audit fails when the recipient's
	// audit information does not match the output token's owner identity.
	t.Run("recipient audit info does not match output", func(t *testing.T) {
		fakeSigningIdentity, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)
		// test idemix info
		_, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		metadata.Outputs[0].OutputAuditInfo = auditInfoRaw
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}}, "1", auditTokens)
		require.Error(t, err)
		require.Contains(t, err.Error(), "owner at index [0] does not match the provided opening")
		require.Contains(t, err.Error(), "does not match the provided opening")
		require.Equal(t, 0, fakeSigningIdentity.SignCallCount())
	})

	// audit an issue tests a successful audit of a valid issue request.
	t.Run("audit an issue", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		ia, metadata := createIssue(t, pp)
		raw, err := ia.Serialize()
		require.NoError(t, err)
		// Issues have no inputs, so pass empty map
		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: metadata}}}, "1", map[string]*token3.Token{})
		require.NoError(t, err)
	})

	// audit a redeem with a valid issuer tests that a redeem transfer approved by an issuer listed
	// in the public parameters is accepted.
	t.Run("audit a redeem with a valid issuer", func(t *testing.T) {
		issuerID, _ := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		_, pp, auditor := setupAuditorTest(t, issuerID)
		transfer, metadata, inputs := createRedeemTransfer(t, pp, issuerID)
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}}, "1", auditTokens)
		require.NoError(t, err)
	})

	// audit a redeem with an unrecognized issuer tests that a redeem transfer is rejected when the
	// approving issuer is not one of the issuers listed in the public parameters.
	t.Run("audit a redeem with an unrecognized issuer", func(t *testing.T) {
		issuerID, _ := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		// setupAuditorTest is called without listing issuerID as an authorized issuer.
		_, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createRedeemTransfer(t, pp, issuerID)
		raw, err := transfer.Serialize()
		require.NoError(t, err)

		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}}, "1", auditTokens)
		require.Error(t, err)
		require.Contains(t, err.Error(), "is not a recognized issuer")
	})
}

// TestAuditor_Errors tests error handling for various Auditor methods, ensuring that the auditor
// correctly identifies and reports inconsistencies in input data and metadata.
func TestAuditor_Errors(t *testing.T) {
	// InspectIdentity error cases tests various error scenarios for identity inspection, such as
	// nil identities, nil audit info, or mismatched identities.
	t.Run("InspectIdentity error cases", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.InspectIdentity(t.Context(), nil, &audit.InspectableIdentity{Identity: nil}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "identity at index [0] is nil")

		err = auditor.InspectIdentity(t.Context(), nil, &audit.InspectableIdentity{Identity: []byte("id")}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "audit info is nil")

		err = auditor.InspectIdentity(t.Context(), nil, &audit.InspectableIdentity{
			Identity:         []byte("id1"),
			IdentityFromMeta: []byte("id2"),
			AuditInfo:        []byte("ai"),
		}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "identity does not match the identity from metadata")
	})

	// InspectIdentity MatchIdentity error tests that an error from the matcher's MatchIdentity method
	// is correctly propagated.
	t.Run("InspectIdentity MatchIdentity error", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		fakeMatcher := &mock.InfoMatcher{}
		fakeMatcher.MatchIdentityReturns(errors.New("match failed"))
		err := auditor.InspectIdentity(t.Context(), fakeMatcher, &audit.InspectableIdentity{
			Identity:  []byte("id"),
			AuditInfo: []byte("ai"),
		}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "match failed")
	})

	// InspectOutput error cases tests error scenarios for output inspection, such as nil outputs
	// or empty tokens.
	t.Run("InspectOutput error cases", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.InspectOutput(t.Context(), nil, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid output at index [0]")

		err = auditor.InspectOutput(t.Context(), &audit.InspectableToken{}, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid output at index [0]")
	})
}

// TestAuditor_Check_Errors tests error handling for the Check method, ensuring the auditor
// correctly identifies and reports errors during the audit of issue and transfer requests.
func TestAuditor_Check_Errors(t *testing.T) {
	// Check issue audit info error tests that an error is returned when audit information for
	// issues cannot be retrieved.
	t.Run("Check issue audit info error", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1, 2, 3}}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}}}}, "1", map[string]*token3.Token{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to deserialize")
	})

	// Check issue request validation error tests that an error is returned when an issue request
	// fails validation (e.g., due to incorrect data).
	t.Run("Check issue request validation error", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		ia, meta := createIssue(t, pp)
		ia.Outputs[0].Data = pp.PedersenGenerators[0] // wrong data
		raw, _ := ia.Serialize()
		err := auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: meta}}}, "1", map[string]*token3.Token{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "output at index [0] does not match the provided opening")
	})

	// Check issue identity validation error tests that an error is returned when an issue identity
	// fails validation (e.g., due to incorrect audit info).
	t.Run("Check issue identity validation error", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		ia, meta := createIssue(t, pp)
		meta.Issuer.AuditInfo = []byte("wrong")
		raw, _ := ia.Serialize()
		err := auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: meta}}}, "1", map[string]*token3.Token{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed checking issuer identity")
	})

	// Check transfer audit info error tests that an error is returned when audit information for
	// transfers cannot be retrieved.
	t.Run("Check transfer audit info error", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(t.Context(), &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{1, 2, 3}}}}, &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: &driver.TransferMetadata{}}}}, "1", map[string]*token3.Token{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to deserialize")
	})
}

// TestAuditor_StructuralValidation tests the new structural validation that ensures
// complete 1:1 correspondence between actions and metadata.
func TestAuditor_StructuralValidation(t *testing.T) {
	// Test action count mismatch
	t.Run("action count mismatch", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		// Request has 2 actions but metadata has only 1
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1}},
					{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{2}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "action count mismatch")
		require.Contains(t, err.Error(), "request has [2] actions but metadata has [1] actions")
	})

	// Test ActionID mismatch
	t.Run("ActionID mismatch", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		// ActionID is 5 but should be 0
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 5, IssueMetadata: &driver.IssueMetadata{}},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "incorrect ActionID")
		require.Contains(t, err.Error(), "[5]")
	})

	// Test action type mismatch - ISSUE action with TransferMetadata
	t.Run("ISSUE action with TransferMetadata", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 0, TransferMetadata: &driver.TransferMetadata{}},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ISSUE but metadata has no IssueMetadata")
	})

	// Test action type mismatch - TRANSFER action with IssueMetadata
	t.Run("TRANSFER action with IssueMetadata", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{1}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "TRANSFER but metadata has no TransferMetadata")
	})

	// Test metadata has both IssueMetadata and TransferMetadata
	t.Run("metadata has both types", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{
						ActionID:         0,
						IssueMetadata:    &driver.IssueMetadata{},
						TransferMetadata: &driver.TransferMetadata{},
					},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "also has TransferMetadata")
	})

	// Test nil action
	t.Run("nil action", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{nil},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "action at index [0] is nil")
	})

	// Test nil metadata
	t.Run("nil metadata", func(t *testing.T) {
		_, _, auditor := setupAuditorTest(t)
		err := auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte{1}},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{nil},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata at index [0] is nil")
	})

	// Test correct ordering with multiple actions
	t.Run("correct ordering with multiple actions", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		// Create valid issue and transfer
		ia, issueMeta := createIssue(t, pp)
		ta, transferMeta, inputs := createTransfer(t, pp)

		issueRaw, err := ia.Serialize()
		require.NoError(t, err)
		transferRaw, err := ta.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from transfer inputs
		auditTokens := buildAuditTokensFromInputs(t, transferMeta, inputs)

		// Test with correct order: issue then transfer
		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: issueRaw},
					{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 0, IssueMetadata: issueMeta},
					{ActionID: 1, TransferMetadata: transferMeta},
				},
			},
			"1", auditTokens,
		)
		require.NoError(t, err)
	})

	// Test wrong ActionID sequence
	t.Run("wrong ActionID sequence", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		ia, issueMeta := createIssue(t, pp)
		issueRaw, err := ia.Serialize()
		require.NoError(t, err)

		// ActionID should be 0 but is 1
		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{
				Actions: []*driver.TypedAction{
					{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: issueRaw},
				},
			},
			&driver.TokenRequestMetadata{
				Actions: []*driver.ActionMetadataEntry{
					{ActionID: 1, IssueMetadata: issueMeta},
				},
			},
			"1", map[string]*token3.Token{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "incorrect ActionID [1]")
	})
	// Test sender count validation - multiple senders
	t.Run("multiple senders in input metadata", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)

		// Add a second sender to the first input
		id2, auditInfoRaw2 := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		metadata.Inputs[0].Senders = append(metadata.Inputs[0].Senders, &driver.AuditableIdentity{
			Identity:  id2,
			AuditInfo: auditInfoRaw2,
		})

		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}},
			&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}},
			"1", auditTokens,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have exactly one sender")
		require.Contains(t, err.Error(), "found [2]")
	})

	// Test sender count validation - no senders
	t.Run("no senders in input metadata", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)

		// Remove all senders from the first input
		metadata.Inputs[0].Senders = []*driver.AuditableIdentity{}

		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}},
			&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}},
			"1", auditTokens,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have exactly one sender")
		require.Contains(t, err.Error(), "found [0]")
	})

	// Test sender identity mismatch
	t.Run("sender identity does not match token owner", func(t *testing.T) {
		_, pp, auditor := setupAuditorTest(t)
		transfer, metadata, inputs := createTransfer(t, pp)

		// Change the sender identity to a different one
		id2, auditInfoRaw2 := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
		metadata.Inputs[0].Senders[0] = &driver.AuditableIdentity{
			Identity:  id2,
			AuditInfo: auditInfoRaw2,
		}

		raw, err := transfer.Serialize()
		require.NoError(t, err)

		// Build auditTokens map from inputs
		auditTokens := buildAuditTokensFromInputs(t, metadata, inputs)

		err = auditor.Check(
			t.Context(),
			&driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}},
			&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}},
			"1", auditTokens,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "sender identity at index [0] does not match token owner")
	})
}

// TestAuditor_TransferInputValidation tests the new validation logic for transfer inputs
func TestAuditor_TransferInputValidation(t *testing.T) {
}

func setupAuditorTest(t *testing.T, issuers ...driver.Identity) (*mock.SigningIdentity, *v1.PublicParams, *audit.AuditorWrapper) {
	t.Helper()
	fakeSigningIdentity := &mock.SigningIdentity{}
	ipk, err := os.ReadFile("./testdata/bls12_381_bbs/idemix/msp/IssuerPublicKey")
	require.NoError(t, err)
	pp, err := v1.Setup(32, ipk, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)
	pp.IssuerIDs = issuers

	deserializer, err := zkatdlog.NewDeserializer(pp)
	require.NoError(t, err)
	auditor := audit.NewAuditorWrapper(
		logging.MustGetLogger(),
		&noop.Tracer{},
		deserializer,
		pp.PedersenGenerators,
		math.Curves[pp.Curve],
		64,
		issuers,
	)
	fakeSigningIdentity.SignReturns([]byte("auditor-signature"), nil)

	return fakeSigningIdentity, pp, auditor
}

func createTransfer(t *testing.T, pp *v1.PublicParams) (*transfer.Action, *driver.TransferMetadata, [][]*token.Token) {
	t.Helper()
	id, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
	transfer, meta, inputs := prepareTransfer(t, pp, id)

	// TokenIDs used in prepareTransfer
	tokenIDs := []*token3.ID{{TxId: "0"}, {TxId: "1"}}

	metadata := &driver.TransferMetadata{}
	for i := range len(transfer.Inputs) {
		metadata.Inputs = append(metadata.Inputs, &driver.TransferInputMetadata{
			TokenID: tokenIDs[i],
			Senders: []*driver.AuditableIdentity{
				{
					Identity:  transfer.Inputs[i].Token.Owner,
					AuditInfo: auditInfoRaw,
				},
			},
		})
	}

	for i := range len(transfer.Outputs) {
		marshalledMeta, err := meta[i].Serialize()
		require.NoError(t, err)
		metadata.Outputs = append(metadata.Outputs, &driver.TransferOutputMetadata{
			OutputMetadata:  marshalledMeta,
			OutputAuditInfo: auditInfoRaw,
			Receivers: []*driver.AuditableIdentity{
				{
					Identity:  transfer.Outputs[i].Owner,
					AuditInfo: auditInfoRaw,
				},
			},
		})
	}

	tokns := make([][]*token.Token, 1)
	tokns[0] = append(tokns[0], inputs...)

	return transfer, metadata, tokns
}

// createRedeemTransfer creates a transfer action with a single redeemed (nil-owner) output,
// approved by the passed issuer identity. Issuer identities are not private in this driver, so no
// audit info is recorded for the issuer in the metadata.
func createRedeemTransfer(t *testing.T, pp *v1.PublicParams, issuerID driver.Identity) (*transfer.Action, *driver.TransferMetadata, [][]*token.Token) {
	t.Helper()
	id, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
	inputs, tokenInfos := createInputs(t, pp, id)

	fakeSigner := &mock.SigningIdentity{}
	sender, err := transfer.NewSender([]driver.Signer{fakeSigner, fakeSigner}, inputs, []*token3.ID{{TxId: "0"}, {TxId: "1"}}, tokenInfos, pp)
	require.NoError(t, err)
	transferAction, meta, err := sender.GenerateZKTransfer(t.Context(), []uint64{60}, [][]byte{nil})
	require.NoError(t, err)
	transferAction.Issuer = issuerID

	tokenIDs := []*token3.ID{{TxId: "0"}, {TxId: "1"}}

	metadata := &driver.TransferMetadata{
		Issuer: driver.AuditableIdentity{Identity: issuerID},
	}
	for i := range len(transferAction.Inputs) {
		metadata.Inputs = append(metadata.Inputs, &driver.TransferInputMetadata{
			TokenID: tokenIDs[i],
			Senders: []*driver.AuditableIdentity{
				{
					Identity:  transferAction.Inputs[i].Token.Owner,
					AuditInfo: auditInfoRaw,
				},
			},
		})
	}

	for i := range len(transferAction.Outputs) {
		marshalledMeta, err := meta[i].Serialize()
		require.NoError(t, err)
		metadata.Outputs = append(metadata.Outputs, &driver.TransferOutputMetadata{
			OutputMetadata:  marshalledMeta,
			OutputAuditInfo: nil,
			Receivers:       nil,
		})
	}

	tokns := make([][]*token.Token, 1)
	tokns[0] = append(tokns[0], inputs...)

	return transferAction, metadata, tokns
}

func createTransferWithBogusOutput(t *testing.T, pp *v1.PublicParams) (*transfer.Action, *driver.TransferMetadata, [][]*token.Token) {
	t.Helper()
	id, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")
	transfer, inf, inputs := prepareTransfer(t, pp, id)

	c := math.Curves[pp.Curve]
	inf[0].Value = c.NewZrFromInt(15)

	// TokenIDs used in prepareTransfer
	tokenIDs := []*token3.ID{{TxId: "0"}, {TxId: "1"}}

	metadata := &driver.TransferMetadata{}
	for i := range len(transfer.Inputs) {
		metadata.Inputs = append(metadata.Inputs, &driver.TransferInputMetadata{
			TokenID: tokenIDs[i],
			Senders: []*driver.AuditableIdentity{
				{
					Identity:  transfer.Inputs[i].Token.Owner,
					AuditInfo: auditInfoRaw,
				},
			},
		})
	}

	for i := range len(transfer.Outputs) {
		marshalledMeta, err := inf[i].Serialize()
		require.NoError(t, err)
		metadata.Outputs = append(metadata.Outputs, &driver.TransferOutputMetadata{
			OutputMetadata:  marshalledMeta,
			OutputAuditInfo: auditInfoRaw,
			Receivers: []*driver.AuditableIdentity{
				{
					Identity:  transfer.Outputs[i].Owner,
					AuditInfo: auditInfoRaw,
				},
			},
		})
	}

	tokns := make([][]*token.Token, 1)
	tokns[0] = append(tokns[0], inputs...)

	return transfer, metadata, tokns
}

func getIdemixInfo(t *testing.T, dir string) (driver.Identity, []byte) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(dir)
	require.NoError(t, err)
	curveID := math.BLS12_381_BBS_GURVY

	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	require.NotNil(t, p)

	identityDescriptor, err := p.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	require.NotNil(t, id)
	require.NotNil(t, audit)

	auditInfo, err := p.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	err = auditInfo.Match(t.Context(), id)
	require.NoError(t, err)

	id, err = identity.WrapWithType(idemix.IdentityType, id)
	require.NoError(t, err)

	return id, audit
}

func createInputs(t *testing.T, pp *v1.PublicParams, id driver.Identity) ([]*token.Token, []*token.Metadata) {
	t.Helper()
	c := math.Curves[pp.Curve]
	inputs := make([]*token.Token, 2)
	infos := make([]*token.Metadata, 2)
	values := []*math.Zr{c.NewZrFromInt(25), c.NewZrFromInt(35)}
	rand, err := c.Rand()
	require.NoError(t, err)
	ttype := c.HashToZr([]byte("ABC"))

	for i := range inputs {
		infos[i] = &token.Metadata{}
		infos[i].BlindingFactor = c.NewRandomZr(rand)
		infos[i].Value = values[i]
		infos[i].Type = "ABC"
		inputs[i] = &token.Token{}
		inputs[i].Data = commit([]*math.Zr{ttype, values[i], infos[i].BlindingFactor}, pp.PedersenGenerators, c)
		inputs[i].Owner = id
	}

	return inputs, infos
}

func prepareTransfer(t *testing.T, pp *v1.PublicParams, id driver.Identity) (*transfer.Action, []*token.Metadata, []*token.Token) {
	t.Helper()
	inputs, tokenInfos := createInputs(t, pp, id)

	fakeSigner := &mock.SigningIdentity{}

	sender, err := transfer.NewSender([]driver.Signer{fakeSigner, fakeSigner}, inputs, []*token3.ID{{TxId: "0"}, {TxId: "1"}}, tokenInfos, pp)
	require.NoError(t, err)
	transfer, inf, err := sender.GenerateZKTransfer(t.Context(), []uint64{40, 20}, [][]byte{id, id})
	require.NoError(t, err)

	return transfer, inf, inputs
}

func createIssue(t *testing.T, pp *v1.PublicParams) (*issue.Action, *driver.IssueMetadata) {
	t.Helper()
	id, auditInfoRaw := getIdemixInfo(t, "./testdata/bls12_381_bbs/idemix")

	// Create a fake signer for the issuer
	fakeSigner := &mock.SigningIdentity{}
	fakeSigner.SerializeReturns(id, nil)

	// Use the proper Issuer to generate an issue action with proofs
	issuer := issue.NewIssuer(token3.Type("ABC"), fakeSigner, pp)
	ia, tokenMetas, err := issuer.GenerateZKIssue([]uint64{100}, [][]byte{id})
	require.NoError(t, err)
	require.Len(t, tokenMetas, 1)

	// Serialize the token metadata
	metaRaw, err := tokenMetas[0].Serialize()
	require.NoError(t, err)

	metadata := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{Identity: id, AuditInfo: auditInfoRaw},
		Outputs: []*driver.IssueOutputMetadata{{
			OutputMetadata:  metaRaw,
			OutputAuditInfo: auditInfoRaw,
			Receivers:       []*driver.AuditableIdentity{{Identity: id, AuditInfo: auditInfoRaw}},
		}},
	}

	return ia, metadata
}

func commit(vector []*math.Zr, generators []*math.G1, c *math.Curve) *math.G1 {
	com := c.NewG1()
	for i := range vector {
		com.Add(generators[i].Mul(vector[i]))
	}

	return com
}

// buildAuditTokensFromInputs creates an auditTokens map from transfer inputs and metadata
func buildAuditTokensFromInputs(t *testing.T, metadata *driver.TransferMetadata, inputs [][]*token.Token) map[string]*token3.Token {
	t.Helper()
	auditTokens := make(map[string]*token3.Token)

	// Process each action's inputs (in this case, just one action)
	if len(inputs) > 0 && len(metadata.Inputs) > 0 {
		for i, tok := range inputs[0] {
			if i >= len(metadata.Inputs) {
				break
			}
			tokenID := metadata.Inputs[i].TokenID
			if tokenID != nil {
				auditTokens[tokenID.String()] = &token3.Token{
					Type:     "ABC",
					Quantity: "0", // Placeholder - actual value not needed for these tests
					Owner:    tok.Owner,
				}
			}
		}
	}

	return auditTokens
}
