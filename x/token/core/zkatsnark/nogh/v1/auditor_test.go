/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
	"context"
	"testing"
	"time"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// idemixTestdataPath points at the idemix MSP material shared with zkatdlog/nogh/v1.
const idemixTestdataPath = "../../../../../../token/core/zkatdlog/nogh/v1/testdata"

// auditorEnv is a testutils.Env built on BN254/groth16, with real proofs and
// real idemix(nym) owner and x509 issuer identities and audit info.
type auditorEnv struct {
	configuration *testutils.SetupConfiguration
	env           *testutils.Env
	deserializer  driver.Deserializer
}

func newAuditorEnv(t *testing.T) *auditorEnv {
	t.Helper()
	configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           []ecc.ID{ecc.BN254},
		OwnerIdentityType:  idemixnym.IdentityType,
	})
	require.NoError(t, err)
	configuration, err := configurations.GetSetupConfiguration(ecc.BN254)
	require.NoError(t, err)
	env, err := testutils.NewEnv(configuration)
	require.NoError(t, err)
	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	require.NoError(t, err)

	return &auditorEnv{configuration: configuration, env: env, deserializer: deserializer}
}

// auditor returns a fresh AuditorService serving copies of auditTokens.
func (e *auditorEnv) auditor(auditTokens map[string]*token.Token) *zkatsnark.AuditorService {
	qe := &mock.QueryEngine{}
	qe.ListAuditTokensStub = func(_ context.Context, ids ...*token.ID) ([]*token.Token, error) {
		res := make([]*token.Token, len(ids))
		for i, id := range ids {
			tok, ok := auditTokens[id.String()]
			if !ok {
				return nil, errors.Errorf("audit token not found: %s", id)
			}
			res[i] = tok
		}

		return res, nil
	}

	return zkatsnark.NewAuditorService(logging.MustGetLogger(), &upgradePPM{pp: e.configuration.PP}, e.deserializer, qe, noop.NewTracerProvider(), common.AuditRetryConfig{NumRetries: 1})
}

// clone deep-copies a request, its metadata and its audit tokens, so that
// tests can tamper with them without affecting the shared Env.
func clone(t *testing.T, tr *driver.TokenRequest, md *driver.TokenRequestMetadata, auditTokens map[string]*token.Token) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token.Token) {
	t.Helper()
	rawTR, err := tr.Bytes()
	require.NoError(t, err)
	trCopy := &driver.TokenRequest{}
	require.NoError(t, trCopy.FromBytes(rawTR))
	rawMD, err := md.Bytes()
	require.NoError(t, err)
	mdCopy := &driver.TokenRequestMetadata{}
	require.NoError(t, mdCopy.FromBytes(rawMD))
	tokens := make(map[string]*token.Token, len(auditTokens))
	for k, v := range auditTokens {
		c := *v
		tokens[k] = &c
	}

	return trCopy, mdCopy, tokens
}

// updateTransfer applies f to the transfer action at index i of tr.
func updateTransfer(t *testing.T, tr *driver.TokenRequest, i int, f func(a *snarktoken.TransferAction)) {
	t.Helper()
	a := &snarktoken.TransferAction{}
	require.NoError(t, a.Deserialize(tr.Actions[i].Raw))
	f(a)
	raw, err := a.Serialize()
	require.NoError(t, err)
	tr.Actions[i].Raw = raw
}

func tamperNote(t *testing.T, raw []byte, f func(n *snarktoken.Note)) []byte {
	t.Helper()
	n, err := snarktoken.Deserialize(raw)
	require.NoError(t, err)
	f(n)
	out, err := n.Serialize()
	require.NoError(t, err)

	return out
}

// TestAuditorService_RealFixtures checks the auditor against requests built by
// the production issue and transfer services, honest and tampered.
func TestAuditorService_RealFixtures(t *testing.T) {
	e := newAuditorEnv(t)
	env := e.env
	issuerAI := e.configuration.IssuerSigner.AuditInfo
	ownerAI := e.configuration.OwnerIdentity.AuditInfo
	// the tamper tests below swap one audit info for the other: they are only
	// meaningful if the two differ
	require.NotEqual(t, issuerAI, ownerAI)
	stranger, err := testutils.PrepareECDSASigner()
	require.NoError(t, err)

	t.Run("honest", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			tr     *driver.TokenRequest
			md     *driver.TokenRequestMetadata
			inputs map[string]*token.Token
			anchor string
		}{
			{"issue", env.TRWithIssue, env.TRWithIssueMetadata, env.TRWithIssueInputs, env.TRWithIssueTxID},
			{"transfer", env.TRWithTransfer, env.TRWithTransferMetadata, env.TRWithTransferInputs, env.TRWithTransferTxID},
			{"redeem", env.TRWithRedeem, env.TRWithRedeemMetadata, env.TRWithRedeemInputs, env.TRWithRedeemTxID},
			{"swap", env.TRWithSwap, env.TRWithSwapMetadata, env.TRWithSwapInputs, env.TRWithSwapTxID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.NoError(t, e.auditor(tc.inputs).AuditorCheck(t.Context(), tc.tr, tc.md, driver.TokenRequestAnchor(tc.anchor)))
			})
		}
	})

	issueCases := []struct {
		name   string
		tamper func(t *testing.T, md *driver.IssueMetadata)
		want   string
	}{
		{"note value inflated", func(t *testing.T, md *driver.IssueMetadata) {
			t.Helper()
			md.Outputs[0].OutputMetadata = tamperNote(t, md.Outputs[0].OutputMetadata, func(n *snarktoken.Note) { n.Value++ })
		}, "output [0]: note does not open the output commitment"},
		{"owner audit info replaced by the issuer's", func(t *testing.T, md *driver.IssueMetadata) {
			t.Helper()
			require.NotEqual(t, md.Outputs[0].OutputAuditInfo, issuerAI)
			md.Outputs[0].OutputAuditInfo = issuerAI
		}, "failed inspecting owner of output [0]: identity at index [0] does not match the provided audit info"},
		{"receiver audit info replaced by the issuer's", func(t *testing.T, md *driver.IssueMetadata) {
			t.Helper()
			require.NotEqual(t, md.Outputs[0].Receivers[0].AuditInfo, issuerAI)
			md.Outputs[0].Receivers[0].AuditInfo = issuerAI
		}, "failed inspecting receiver [0] of output [0]"},
		{"issuer audit info replaced by the owner's", func(t *testing.T, md *driver.IssueMetadata) {
			t.Helper()
			require.NotEqual(t, md.Issuer.AuditInfo, ownerAI)
			md.Issuer.AuditInfo = ownerAI
		}, "failed checking issuer identity"},
		{"note issuer replaced", func(t *testing.T, md *driver.IssueMetadata) {
			t.Helper()
			md.Outputs[0].OutputMetadata = tamperNote(t, md.Outputs[0].OutputMetadata, func(n *snarktoken.Note) { n.Issuer = stranger.ID })
		}, "output [0]: note issuer differs from the action issuer"},
	}
	for _, tc := range issueCases {
		t.Run("issue/"+tc.name, func(t *testing.T) {
			tr, md, inputs := clone(t, env.TRWithIssue, env.TRWithIssueMetadata, env.TRWithIssueInputs)
			tc.tamper(t, md.Actions[0].IssueMetadata)
			require.ErrorContains(t, e.auditor(inputs).AuditorCheck(t.Context(), tr, md, driver.TokenRequestAnchor(env.TRWithIssueTxID)), tc.want)
		})
	}

	transferCases := []struct {
		name   string
		redeem bool
		tamper func(t *testing.T, tr *driver.TokenRequest, md *driver.TransferMetadata, inputs map[string]*token.Token)
		want   string
	}{
		{"value inflation in transfer metadata", false, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			md.Outputs[0].OutputMetadata = tamperNote(t, md.Outputs[0].OutputMetadata, func(n *snarktoken.Note) { n.Value++ })
		}, "output [0]: note does not open the output commitment"},
		{"owner audit info replaced by the issuer's", false, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			require.NotEqual(t, md.Outputs[0].OutputAuditInfo, issuerAI)
			md.Outputs[0].OutputAuditInfo = issuerAI
		}, "failed inspecting owner of output [0]: identity at index [0] does not match the provided audit info"},
		{"sender audit info replaced by the issuer's", false, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			require.NotEqual(t, md.Inputs[0].Senders[0].AuditInfo, issuerAI)
			md.Inputs[0].Senders[0].AuditInfo = issuerAI
		}, "failed inspecting sender of input [0]: identity at index [0] does not match the provided audit info"},
		{"receiver identity replaced", false, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			md.Outputs[0].Receivers[0] = &driver.AuditableIdentity{Identity: stranger.ID, AuditInfo: stranger.AuditInfo}
		}, "recipient [0] of output [0] does not match the receiver identity in metadata"},
		{"swapped owner", false, func(t *testing.T, tr *driver.TokenRequest, _ *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			updateTransfer(t, tr, 0, func(a *snarktoken.TransferAction) { a.Outputs[0].Recipient = stranger.ID })
		}, "failed inspecting owner of output [0]"},
		{"audited input quantity lowered", false, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, inputs map[string]*token.Token) {
			t.Helper()
			tok := inputs[md.Inputs[0].TokenID.String()]
			q, err := token.ToQuantity(tok.Quantity, 64)
			require.NoError(t, err)
			tok.Quantity = token.NewQuantityFromUInt64(q.ToBigInt().Uint64() - 1).Hex()
		}, "differs from the input value"},
		{"notes swapped between outputs", true, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			md.Outputs[0].OutputMetadata, md.Outputs[1].OutputMetadata = md.Outputs[1].OutputMetadata, md.Outputs[0].OutputMetadata
		}, "output [0]: note does not open the output commitment"},
		{"unauthorized redeem issuer", true, func(t *testing.T, tr *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			require.NotEmpty(t, md.Issuer.Identity)
			updateTransfer(t, tr, 0, func(a *snarktoken.TransferAction) { a.Issuer = stranger.ID })
			md.Issuer.Identity = stranger.ID
		}, "is not a recognized issuer"},
		{"burned value misreported", true, func(t *testing.T, _ *driver.TokenRequest, md *driver.TransferMetadata, _ map[string]*token.Token) {
			t.Helper()
			md.Outputs[0].OutputMetadata = tamperNote(t, md.Outputs[0].OutputMetadata, func(n *snarktoken.Note) { n.Value-- })
		}, "output [0]: note does not open the output commitment"},
	}
	for _, tc := range transferCases {
		t.Run("transfer/"+tc.name, func(t *testing.T) {
			src, srcMD, srcInputs, anchor := env.TRWithTransfer, env.TRWithTransferMetadata, env.TRWithTransferInputs, env.TRWithTransferTxID
			if tc.redeem {
				src, srcMD, srcInputs, anchor = env.TRWithRedeem, env.TRWithRedeemMetadata, env.TRWithRedeemInputs, env.TRWithRedeemTxID
			}
			tr, md, inputs := clone(t, src, srcMD, srcInputs)
			tc.tamper(t, tr, md.Actions[0].TransferMetadata, inputs)
			require.ErrorContains(t, e.auditor(inputs).AuditorCheck(t.Context(), tr, md, driver.TokenRequestAnchor(anchor)), tc.want)
		})
	}

	t.Run("swap/second action tampered", func(t *testing.T) {
		tr, md, inputs := clone(t, env.TRWithSwap, env.TRWithSwapMetadata, env.TRWithSwapInputs)
		out := md.Actions[1].TransferMetadata.Outputs[0]
		out.OutputMetadata = tamperNote(t, out.OutputMetadata, func(n *snarktoken.Note) { n.Value++ })
		err := e.auditor(inputs).AuditorCheck(t.Context(), tr, md, driver.TokenRequestAnchor(env.TRWithSwapTxID))
		require.ErrorContains(t, err, "failed to check transfer action at [1]")
		require.ErrorContains(t, err, "output [0]: note does not open the output commitment")
	})
}

// TestNewAuditorService_RetryConfig checks that the per-TMS audit retry
// configuration is honored.
func TestNewAuditorService_RetryConfig(t *testing.T) {
	cfg := common.AuditRetryConfig{NumRetries: 7, RetryDelay: 42 * time.Millisecond}
	s := zkatsnark.NewAuditorService(logging.MustGetLogger(), nil, nil, nil, noop.NewTracerProvider(), cfg)
	require.Equal(t, 7, s.AuditTokensNumRetries)
	require.Equal(t, 42*time.Millisecond, s.AuditTokensRetryDelay)

	def := zkatsnark.NewAuditorService(logging.MustGetLogger(), nil, nil, nil, noop.NewTracerProvider(), common.DefaultAuditRetryConfig())
	require.Equal(t, common.DefaultAuditTokensNumRetries, def.AuditTokensNumRetries)
	require.Equal(t, common.DefaultAuditTokensRetryDelay, def.AuditTokensRetryDelay)
}
