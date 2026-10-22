/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverMock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// ledger is an in-memory token2.ID -> serialized OutputDescription store,
// shared by every fixture built within a single Env. TransferSignatureValidate
// looks up each transfer input's on-chain OutputDescription via
// ctx.Ledger.GetState (unlike nogh's validator, which never consults the
// ledger directly), so every note issued as a transfer input by this package
// must be recorded here for re-validation to succeed.
type ledger struct {
	tokens map[string][]byte
}

func newLedger() *ledger {
	return &ledger{tokens: make(map[string][]byte)}
}

// GetState implements driver.GetStateFnc.
func (l *ledger) GetState(id token2.ID) ([]byte, error) {
	raw, ok := l.tokens[id.String()]
	if !ok {
		return nil, errors.Errorf("token not found: %s", id.String())
	}

	return raw, nil
}

func (l *ledger) store(id *token2.ID, raw []byte) {
	l.tokens[id.String()] = raw
}

// Env holds every fixture built against the real zkatsnark production
// services, mirroring token/core/zkatdlog/nogh/v1/testutils.Env field for
// field (minus the upgrade-witness fixture, which has no zkatsnark
// equivalent — see plan notes).
type Env struct {
	Engine *validator.Validator

	// Ledger backs every TRWith* fixture's transfer inputs. Pass Ledger.GetState
	// as the getState argument to Engine.VerifyTokenRequestFromRaw when
	// re-validating any fixture produced by this Env.
	Ledger *ledger

	TRWithTransferTxID     string
	TRWithTransfer         *driver.TokenRequest
	TRWithTransferRaw      []byte
	TRWithTransferMetadata *driver.TokenRequestMetadata
	TRWithTransferInputs   map[string]*token2.Token

	TRWithRedeem         *driver.TokenRequest
	TRWithRedeemTxID     string
	TRWithRedeemRaw      []byte
	TRWithRedeemMetadata *driver.TokenRequestMetadata
	TRWithRedeemInputs   map[string]*token2.Token

	TRWithIssue         *driver.TokenRequest
	TRWithIssueTxID     string
	TRWithIssueRaw      []byte
	TRWithIssueMetadata *driver.TokenRequestMetadata
	TRWithIssueInputs   map[string]*token2.Token

	TRWithSwap         *driver.TokenRequest
	TRWithSwapTxID     string
	TRWithSwapRaw      []byte
	TRWithSwapMetadata *driver.TokenRequestMetadata
	TRWithSwapInputs   map[string]*token2.Token

	TRWithPublicMetadataIssue         *driver.TokenRequest
	TRWithPublicMetadataIssueTxID     string
	TRWithPublicMetadataIssueRaw      []byte
	TRWithPublicMetadataIssueMetadata *driver.TokenRequestMetadata
	TRWithPublicMetadataIssueInputs   map[string]*token2.Token

	TRWithPublicMetadataTransfer         *driver.TokenRequest
	TRWithPublicMetadataTransferTxID     string
	TRWithPublicMetadataTransferRaw      []byte
	TRWithPublicMetadataTransferMetadata *driver.TokenRequestMetadata
	TRWithPublicMetadataTransferInputs   map[string]*token2.Token

	// TRWithUnclaimedMetadataTransfer is a NEGATIVE fixture: its metadata
	// carries a key that no validator branch claims, so re-validation must
	// fail with "more metadata than those validated".
	TRWithUnclaimedMetadataTransfer         *driver.TokenRequest
	TRWithUnclaimedMetadataTransferTxID     string
	TRWithUnclaimedMetadataTransferRaw      []byte
	TRWithUnclaimedMetadataTransferMetadata *driver.TokenRequestMetadata
	TRWithUnclaimedMetadataTransferInputs   map[string]*token2.Token

	TRWithMultiAuditorTransfer         *driver.TokenRequest
	TRWithMultiAuditorTransferTxID     string
	TRWithMultiAuditorTransferRaw      []byte
	TRWithMultiAuditorTransferMetadata *driver.TokenRequestMetadata
	TRWithMultiAuditorTransferInputs   map[string]*token2.Token

	// TRWithExtraSignatureTransfer is a NEGATIVE fixture: it carries one
	// extra, unconsumed action signature, so re-validation must fail with
	// "unconsumed signatures".
	TRWithExtraSignatureTransfer         *driver.TokenRequest
	TRWithExtraSignatureTransferTxID     string
	TRWithExtraSignatureTransferRaw      []byte
	TRWithExtraSignatureTransferMetadata *driver.TokenRequestMetadata
	TRWithExtraSignatureTransferInputs   map[string]*token2.Token
}

// NewEnv builds an Env for the given SetupConfiguration.
func NewEnv(configuration *SetupConfiguration) (*Env, error) {
	pp := configuration.PP

	deserializer, err := zkatsnarkdriver.NewDeserializer(pp)
	if err != nil {
		return nil, err
	}

	engine, err := validator.NewValidator(configuration.Verifier, deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, err
	}

	l := newLedger()

	// non-anonymous issue
	ir, irMetadata, err := prepareIssueRequest(configuration)
	if err != nil {
		return nil, err
	}
	irRaw, err := ir.Bytes()
	if err != nil {
		return nil, err
	}

	// redeem
	rr, rrMetadata, rrInputs, err := prepareRedeemRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	rrRaw, err := rr.Bytes()
	if err != nil {
		return nil, err
	}

	// transfer
	tr, trMetadata, trInputs, err := prepareTransferRequest(configuration, l, configuration.AuditorSigner)
	if err != nil {
		return nil, err
	}
	transferRaw, err := tr.Bytes()
	if err != nil {
		return nil, err
	}

	// public-metadata issue
	pmir, pmirMeta, err := preparePublicMetadataIssueRequest(configuration)
	if err != nil {
		return nil, err
	}
	publicMetadataIssueRaw, err := pmir.Bytes()
	if err != nil {
		return nil, err
	}

	// public-metadata transfer
	pmtr, pmtrMeta, pmtrInputs, err := preparePublicMetadataTransferRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	publicMetadataTransferRaw, err := pmtr.Bytes()
	if err != nil {
		return nil, err
	}

	// unclaimed-metadata transfer (negative fixture)
	umtr, umtrMeta, umtrInputs, err := prepareUnclaimedMetadataTransferRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	unclaimedMetadataTransferRaw, err := umtr.Bytes()
	if err != nil {
		return nil, err
	}

	// multi-auditor transfer
	matr, matrMeta, matrInputs, err := prepareMultiAuditorTransferRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	multiAuditorTransferRaw, err := matr.Bytes()
	if err != nil {
		return nil, err
	}

	// extra-signature transfer (negative fixture)
	estr, estrMeta, estrInputs, err := prepareExtraSignatureTransferRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	extraSignatureTransferRaw, err := estr.Bytes()
	if err != nil {
		return nil, err
	}

	// atomic swap request
	ar, arMetadata, arInputs, err := prepareSwapRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	arRaw, err := ar.Bytes()
	if err != nil {
		return nil, err
	}

	return &Env{
		Engine: engine,
		Ledger: l,

		TRWithTransferTxID:     "1",
		TRWithTransfer:         tr,
		TRWithTransferRaw:      transferRaw,
		TRWithTransferMetadata: trMetadata,
		TRWithTransferInputs:   trInputs,

		TRWithPublicMetadataIssue:         pmir,
		TRWithPublicMetadataIssueTxID:     "1",
		TRWithPublicMetadataIssueRaw:      publicMetadataIssueRaw,
		TRWithPublicMetadataIssueMetadata: pmirMeta,
		TRWithPublicMetadataIssueInputs:   map[string]*token2.Token{},

		TRWithPublicMetadataTransfer:         pmtr,
		TRWithPublicMetadataTransferTxID:     "1",
		TRWithPublicMetadataTransferRaw:      publicMetadataTransferRaw,
		TRWithPublicMetadataTransferMetadata: pmtrMeta,
		TRWithPublicMetadataTransferInputs:   pmtrInputs,

		TRWithUnclaimedMetadataTransfer:         umtr,
		TRWithUnclaimedMetadataTransferTxID:     "1",
		TRWithUnclaimedMetadataTransferRaw:      unclaimedMetadataTransferRaw,
		TRWithUnclaimedMetadataTransferMetadata: umtrMeta,
		TRWithUnclaimedMetadataTransferInputs:   umtrInputs,

		TRWithMultiAuditorTransfer:         matr,
		TRWithMultiAuditorTransferTxID:     "1",
		TRWithMultiAuditorTransferRaw:      multiAuditorTransferRaw,
		TRWithMultiAuditorTransferMetadata: matrMeta,
		TRWithMultiAuditorTransferInputs:   matrInputs,

		TRWithExtraSignatureTransfer:         estr,
		TRWithExtraSignatureTransferTxID:     "1",
		TRWithExtraSignatureTransferRaw:      extraSignatureTransferRaw,
		TRWithExtraSignatureTransferMetadata: estrMeta,
		TRWithExtraSignatureTransferInputs:   estrInputs,

		TRWithRedeem:         rr,
		TRWithRedeemTxID:     "1",
		TRWithRedeemRaw:      rrRaw,
		TRWithRedeemMetadata: rrMetadata,
		TRWithRedeemInputs:   rrInputs,

		TRWithIssue:         ir,
		TRWithIssueTxID:     "1",
		TRWithIssueRaw:      irRaw,
		TRWithIssueMetadata: irMetadata,
		TRWithIssueInputs:   map[string]*token2.Token{},

		TRWithSwap:         ar,
		TRWithSwapTxID:     "2",
		TRWithSwapRaw:      arRaw,
		TRWithSwapMetadata: arMetadata,
		TRWithSwapInputs:   arInputs,
	}, nil
}

// SaveTransferToFile writes TRWithTransferTxID, TRWithTransferRaw, TRWithTransferMetadata,
// and TRWithTransferInputs into the provided path as JSON.
func (e *Env) SaveTransferToFile(path string) error {
	return e.saveToFile(path, e.TRWithTransferTxID, e.TRWithTransferRaw, e.TRWithTransferMetadata, e.TRWithTransferInputs)
}

// SaveIssueToFile writes TRWithIssueTxID, TRWithIssueRaw, TRWithIssueMetadata,
// and TRWithIssueInputs into the provided path as JSON.
func (e *Env) SaveIssueToFile(path string) error {
	return e.saveToFile(path, e.TRWithIssueTxID, e.TRWithIssueRaw, e.TRWithIssueMetadata, e.TRWithIssueInputs)
}

// SaveRedeemToFile writes TRWithRedeemTxID, TRWithRedeemRaw, TRWithRedeemMetadata,
// and TRWithRedeemInputs into the provided path as JSON.
func (e *Env) SaveRedeemToFile(path string) error {
	return e.saveToFile(path, e.TRWithRedeemTxID, e.TRWithRedeemRaw, e.TRWithRedeemMetadata, e.TRWithRedeemInputs)
}

// SaveSwapToFile writes TRWithSwapTxID, TRWithSwapRaw, TRWithSwapMetadata,
// and TRWithSwapInputs into the provided path as JSON.
func (e *Env) SaveSwapToFile(path string) error {
	return e.saveToFile(path, e.TRWithSwapTxID, e.TRWithSwapRaw, e.TRWithSwapMetadata, e.TRWithSwapInputs)
}

// Inputs wraps a map of token IDs to tokens for JSON (de)serialization.
type Inputs struct {
	Inputs map[string]*token2.Token
}

func (i *Inputs) MarshalJSON() ([]byte, error) {
	t := map[string]token2.Token{}
	for k, v := range i.Inputs {
		t[k] = *v
	}

	return json.Marshal(t)
}

func (i *Inputs) UnmarshalJSON(b []byte) error {
	i.Inputs = make(map[string]*token2.Token)
	t := map[string]token2.Token{}
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	for k, v := range t {
		i.Inputs[k] = &v
	}

	return nil
}

// TestCase represents a single test case with all its data.
type TestCase struct {
	TxID     string  `json:"txid"`
	ReqRaw   string  `json:"req_raw"`
	Metadata string  `json:"metadata,omitempty"`
	Inputs   *Inputs `json:"inputs,omitempty"`
}

// SaveAggregatedToFile writes multiple test cases to a single JSON file.
// The cases parameter is a map where the key is the test case index (e.g., "0", "1", etc.)
// and the value is the TestCase data.
func SaveAggregatedToFile(path string, cases map[string]*TestCase) error {
	b, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal aggregated test cases")
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		return errors.Wrap(err, "failed to write aggregated file")
	}

	return nil
}

// TransferToTestCase converts the Env's transfer data to a TestCase.
func (e *Env) TransferToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithTransferTxID, e.TRWithTransferRaw, e.TRWithTransferMetadata, e.TRWithTransferInputs)
}

// PublicMetadataIssueToTestCase converts the Env's public-metadata issue data to a TestCase.
func (e *Env) PublicMetadataIssueToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithPublicMetadataIssueTxID, e.TRWithPublicMetadataIssueRaw, e.TRWithPublicMetadataIssueMetadata, e.TRWithPublicMetadataIssueInputs)
}

// PublicMetadataTransferToTestCase converts the Env's public-metadata transfer data to a TestCase.
func (e *Env) PublicMetadataTransferToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithPublicMetadataTransferTxID, e.TRWithPublicMetadataTransferRaw, e.TRWithPublicMetadataTransferMetadata, e.TRWithPublicMetadataTransferInputs)
}

// UnclaimedMetadataToTestCase converts the Env's unclaimed-metadata transfer
// data to a TestCase. This is a NEGATIVE fixture: validation is expected to
// fail with "more metadata than those validated".
func (e *Env) UnclaimedMetadataToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithUnclaimedMetadataTransferTxID, e.TRWithUnclaimedMetadataTransferRaw, e.TRWithUnclaimedMetadataTransferMetadata, e.TRWithUnclaimedMetadataTransferInputs)
}

// MultiAuditorTransferToTestCase converts the Env's multi-auditor transfer data to a TestCase.
func (e *Env) MultiAuditorTransferToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithMultiAuditorTransferTxID, e.TRWithMultiAuditorTransferRaw, e.TRWithMultiAuditorTransferMetadata, e.TRWithMultiAuditorTransferInputs)
}

// ExtraSignatureToTestCase converts the Env's extra-signature transfer data
// to a TestCase. This is a NEGATIVE fixture: validation is expected to fail
// with "unconsumed signatures".
func (e *Env) ExtraSignatureToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithExtraSignatureTransferTxID, e.TRWithExtraSignatureTransferRaw, e.TRWithExtraSignatureTransferMetadata, e.TRWithExtraSignatureTransferInputs)
}

// IssueToTestCase converts the Env's issue data to a TestCase.
func (e *Env) IssueToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithIssueTxID, e.TRWithIssueRaw, e.TRWithIssueMetadata, e.TRWithIssueInputs)
}

// RedeemToTestCase converts the Env's redeem data to a TestCase.
func (e *Env) RedeemToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithRedeemTxID, e.TRWithRedeemRaw, e.TRWithRedeemMetadata, e.TRWithRedeemInputs)
}

// SwapToTestCase converts the Env's swap data to a TestCase.
func (e *Env) SwapToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithSwapTxID, e.TRWithSwapRaw, e.TRWithSwapMetadata, e.TRWithSwapInputs)
}

func (e *Env) toTestCase(txID string, raw []byte, metadata *driver.TokenRequestMetadata, inputs map[string]*token2.Token) (*TestCase, error) {
	if e == nil {
		return nil, errors.Errorf("nil Env")
	}

	return buildTestCase(txID, raw, metadata, inputs)
}

func buildTestCase(txID string, raw []byte, metadata *driver.TokenRequestMetadata, inputs map[string]*token2.Token) (*TestCase, error) {
	tc := &TestCase{
		TxID:   txID,
		ReqRaw: base64.StdEncoding.EncodeToString(raw),
	}

	if metadata != nil {
		metadataBytes, err := metadata.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal metadata")
		}
		tc.Metadata = base64.StdEncoding.EncodeToString(metadataBytes)
	}
	tc.Inputs = &Inputs{
		Inputs: inputs,
	}

	return tc, nil
}

func (e *Env) saveToFile(path string, txID string, raw []byte, metadata *driver.TokenRequestMetadata, inputs map[string]*token2.Token) error {
	if e == nil {
		return errors.Errorf("nil Env")
	}

	var metadataEncoded string
	if metadata != nil {
		metadataBytes, err := metadata.Bytes()
		if err != nil {
			return errors.Wrap(err, "failed to marshal metadata")
		}
		metadataEncoded = base64.StdEncoding.EncodeToString(metadataBytes)
	}

	payload := TestCase{
		TxID:     txID,
		ReqRaw:   base64.StdEncoding.EncodeToString(raw),
		Metadata: metadataEncoded,
		Inputs:   &Inputs{Inputs: inputs},
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}

	return nil
}

// ── Fixture builders ────────────────────────────────────────────────────────

// newIssueService builds a zkatsnark.IssueService wired against the given
// configuration, using the generic test scaffolding from support.go.
func newIssueService(configuration *SetupConfiguration) (*zkatsnark.IssueService, driver.Deserializer, error) {
	ppm := &testPublicParamsManager{pp: configuration.PP}
	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create deserializer")
	}
	ws := &testWalletService{
		issuerSigner: configuration.IssuerSigner,
		auditInfoMap: map[string][]byte{
			configuration.IssuerSigner.ID.String():  configuration.IssuerSigner.AuditInfo,
			configuration.OwnerIdentity.ID.String(): configuration.OwnerIdentity.AuditInfo,
		},
	}

	v, err := validator.NewValidator(configuration.Verifier, deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to instantiate validator")
	}

	return zkatsnark.NewIssueService(
		logging.MustGetLogger(),
		ppm,
		ws,
		deserializer,
		nil, // TokensService is not required by IssueService.Issue/VerifyIssue
		nil, // TokensUpgradeService: out of scope, see plan notes
		configuration.Orchestrator,
		v,
	), deserializer, nil
}

// prepareIssueRequest builds a regular (non-anonymous) issue request.
func prepareIssueRequest(configuration *SetupConfiguration) (*driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	return prepareIssueRequestWithAttrs(configuration, nil)
}

// preparePublicMetadataIssueRequest builds a regular issue request carrying a
// "pub."-prefixed application metadata attribute, so that
// IssueApplicationDataValidate's metadata-claiming branch is exercised
// end-to-end.
func preparePublicMetadataIssueRequest(configuration *SetupConfiguration) (*driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	attrs := map[string]any{
		meta.IssueMetadataPrefix + meta.PublicMetadataPrefix + "note": []byte("public issue metadata"),
	}

	return prepareIssueRequestWithAttrs(configuration, attrs)
}

// issuedNote bundles the OutputDescription and Note produced by a single
// issue output, together with the ledger token ID it was issued under.
type issuedNote struct {
	ID     *token2.ID
	Output snarktoken.OutputDescription
	Note   *snarktoken.Note
}

// issueNotes issues real zkatsnark notes via IssueService.Issue, for use as
// transfer inputs. zkatsnark inputs must be real Note+OutputDescription pairs
// produced by the orchestrator/prover: there is no zkatdlog-style "build a
// Pedersen commitment by hand" shortcut.
func issueNotes(configuration *SetupConfiguration, txID string, values []uint64) ([]issuedNote, error) {
	issueService, _, err := newIssueService(configuration)
	if err != nil {
		return nil, err
	}

	issuerIdentity, err := configuration.IssuerSigner.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize issuer identity")
	}

	owners := make([][]byte, len(values))
	for i := range values {
		owners[i] = configuration.OwnerIdentity.ID
	}

	action, issueMetadata, err := issueService.Issue(context.Background(), issuerIdentity, "ABC", values, owners, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to issue input notes")
	}
	issueAction, ok := action.(*snarktoken.IssueAction)
	if !ok {
		return nil, errors.Errorf("expected *snarktoken.IssueAction, got [%T]", action)
	}

	notes := make([]issuedNote, len(values))
	for i := range values {
		note, err := snarktoken.Deserialize(issueMetadata.Outputs[i].OutputMetadata)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deserialize issued note")
		}
		notes[i] = issuedNote{
			ID:     &token2.ID{TxId: txID, Index: uint64(i)},
			Output: issueAction.Outputs[i],
			Note:   note,
		}
	}

	return notes, nil
}

// prepareIssueRequestWithAttrs builds and signs an issue token request,
// endorsed by the auditor, for the given optional application metadata attrs.
func prepareIssueRequestWithAttrs(configuration *SetupConfiguration, attrs map[string]any) (*driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	issueService, deserializer, err := newIssueService(configuration)
	if err != nil {
		return nil, nil, err
	}

	issuerIdentity, err := configuration.IssuerSigner.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize issuer identity")
	}

	owners := [][]byte{configuration.OwnerIdentity.ID}
	values := []uint64{40}

	var issueOpts *driver.IssueOptions
	if len(attrs) > 0 {
		issueOpts = &driver.IssueOptions{Attributes: attrs}
	}

	issueAction, issueMetadata, err := issueService.Issue(context.Background(), issuerIdentity, "ABC", values, owners, issueOpts)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to issue tokens")
	}

	raw, err := issueAction.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize issue action")
	}

	ir := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw},
		},
	}

	rawToSign, err := ir.MarshalToMessageToSign([]byte("1"))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to marshal token request")
	}

	sig, err := configuration.IssuerSigner.Sign(rawToSign)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to sign token actions")
	}

	requestMetadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{ActionID: 0, IssueMetadata: issueMetadata},
		},
	}

	// Auditor check: issues have no inputs, so the query engine mock is never
	// invoked (RetrieveAuditTokens short-circuits on an empty token ID list).
	auditorService := newAuditorService(configuration, deserializer, nil)
	if err := auditorService.AuditorCheck(context.Background(), ir, requestMetadata, "1"); err != nil {
		return nil, nil, errors.Wrap(err, "auditor check failed")
	}

	sigma, err := auditorEndorse(configuration.AuditorSigner, ir, "1")
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get auditor endorsement")
	}
	araw, err := configuration.AuditorSigner.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize auditor identity")
	}

	ir.Signatures = append(ir.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  araw,
			Signature: sigma,
		},
	})
	ir.Signatures = append(ir.Signatures, &driver.RequestSignature{
		Action: &driver.ActionSignature{
			ActionID:  0,
			Signature: sig,
		},
	})

	return ir, requestMetadata, nil
}

// newAuditorService builds a zkatsnark.AuditorService wired against the
// given configuration and a QueryEngine mock stubbed to serve the given
// audit tokens (keyed by token ID string). A nil/empty auditTokens map is
// fine for issue-only fixtures, since AuditorCheck never queries the engine
// when there are no inputs to audit.
func newAuditorService(configuration *SetupConfiguration, deserializer driver.Deserializer, auditTokens map[string]*token2.Token) *zkatsnark.AuditorService {
	ppm := &testPublicParamsManager{pp: configuration.PP}
	qe := &driverMock.QueryEngine{}
	qe.ListAuditTokensStub = func(ctx context.Context, ids ...*token2.ID) ([]*token2.Token, error) {
		tokens := make([]*token2.Token, len(ids))
		for i, id := range ids {
			tok, ok := auditTokens[id.String()]
			if !ok {
				return nil, errors.Errorf("audit token not found: %s", id.String())
			}
			tokens[i] = tok
		}

		return tokens, nil
	}

	return zkatsnark.NewAuditorService(
		logging.MustGetLogger(),
		ppm,
		deserializer,
		qe,
		noop.NewTracerProvider(),
	)
}

// newTransferService builds a zkatsnark.TransferService wired against the
// given configuration and token loader.
func newTransferService(configuration *SetupConfiguration, deserializer driver.Deserializer, loader *testTokenLoader, auditInfoMap map[string][]byte) (*zkatsnark.TransferService, error) {
	ppm := &testPublicParamsManager{pp: configuration.PP}
	ws := &testWalletService{auditInfoMap: auditInfoMap}

	v, err := validator.NewValidator(configuration.Verifier, deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}

	return zkatsnark.NewTransferService(
		logging.MustGetLogger(),
		ppm,
		ws,
		loader,
		deserializer,
		noop.NewTracerProvider(),
		nil, // TokensService is not required by TransferService.Transfer/VerifyTransfer
		nil, // tokenDeserializer: placeholder, unused by TransferService
		configuration.Orchestrator,
		v,
	), nil
}

// transferTokenType is the token type used by every fixture in this package.
const transferTokenType = "ABC"

// issueTxIDCounter guarantees each prepareTransferWithOpts call issues its
// input notes under a distinct ledger transaction ID, so that two
// independently built transfers (e.g. the two legs of a swap) never collide
// on the same token ID when combined into one TokenRequest.
var issueTxIDCounter atomic.Uint64

func nextIssueTxID() string {
	return "issue-for-transfer-" + strconv.FormatUint(issueTxIDCounter.Add(1), 10)
}

// prepareTransferWithOpts is the shared builder behind every positive and
// negative transfer fixture. It issues numInputs real input notes (summing
// to a value evenly divisible across numOutputs outputs, mirroring nogh's
// value bookkeeping), builds a TransferService.Transfer call against them,
// and signs/endorses the resulting request. owners must have len ==
// numOutputs; an empty owners[0] produces a redeem (burn) output.
func prepareTransferWithOpts(
	configuration *SetupConfiguration,
	l *ledger,
	numInputs, numOutputs int,
	owners [][]byte,
	auditorSigner *Signer,
	attrs map[string]any,
) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	if numOutputs <= 0 || len(owners) != numOutputs {
		return nil, nil, nil, errors.Errorf("invalid number of outputs [%d]", numOutputs)
	}

	// Issue real input notes.
	inValues := make([]uint64, numInputs)
	sumInputs := uint64(0)
	for i := range inValues {
		v := uint64(i*10 + 500)
		inValues[i] = v
		sumInputs += v
	}
	notes, err := issueNotes(configuration, nextIssueTxID(), inValues)
	if err != nil {
		return nil, nil, nil, err
	}

	outputValue := sumInputs / uint64(numOutputs)
	sumOutputs := uint64(0)
	outValues := make([]uint64, numOutputs)
	for i := range numOutputs {
		outValues[i] = outputValue
		sumOutputs += outputValue
	}
	if delta := sumInputs - sumOutputs; delta > 0 {
		outValues[0] += delta
	}

	ids := make([]*token2.ID, numInputs)
	tokenLoaderMap := make(map[string]common.LoadedToken[[]byte, []byte])
	auditTokens := make(map[string]*token2.Token)
	auditInfoMap := make(map[string][]byte)
	for i, n := range notes {
		ids[i] = n.ID

		outputRaw, err := n.Output.Serialize()
		if err != nil {
			return nil, nil, nil, errors.Wrap(err, "failed to serialize input output description")
		}
		metadataRaw, err := n.Note.Serialize()
		if err != nil {
			return nil, nil, nil, errors.Wrap(err, "failed to serialize input note")
		}
		tokenLoaderMap[n.ID.String()] = common.LoadedToken[[]byte, []byte]{
			TokenFormat: "zkatsnark",
			Token:       outputRaw,
			Metadata:    metadataRaw,
		}
		l.store(n.ID, outputRaw)

		auditTokens[n.ID.String()] = &token2.Token{
			Type:     transferTokenType,
			Quantity: token2.NewQuantityFromUInt64(inValues[i]).Hex(),
			Owner:    n.Output.Recipient,
		}
		auditInfoMap[string(n.Output.Recipient)] = configuration.OwnerIdentity.AuditInfo
	}
	for _, owner := range owners {
		if len(owner) > 0 {
			auditInfoMap[string(owner)] = configuration.OwnerIdentity.AuditInfo
		}
	}

	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create deserializer")
	}
	loader := &testTokenLoader{tokens: tokenLoaderMap}
	transferService, err := newTransferService(configuration, deserializer, loader, auditInfoMap)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create transfer service")
	}

	outputTokens := make([]*token2.Token, numOutputs)
	for i := range numOutputs {
		outputTokens[i] = &token2.Token{
			Type:     transferTokenType,
			Quantity: token2.NewQuantityFromUInt64(outValues[i]).Hex(),
			Owner:    owners[i],
		}
	}

	ownerWallet := &testOwnerWallet{id: "test-owner-wallet", signer: configuration.OwnerIdentity.Signer}

	transferOpts := &driver.TransferOptions{}
	if len(attrs) > 0 {
		transferOpts.Attributes = attrs
	}
	transferAction, transferMetadata, err := transferService.Transfer(
		context.Background(),
		"1", // anchor (txID)
		ownerWallet,
		ids,
		outputTokens,
		transferOpts,
	)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to generate transfer using TransferService")
	}

	transferRaw, err := transferAction.Serialize()
	if err != nil {
		return nil, nil, nil, err
	}

	tr := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
		},
	}
	raw, err := tr.MarshalToMessageToSign([]byte("1"))
	if err != nil {
		return nil, nil, nil, err
	}

	tokenRequestMetadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{ActionID: 0, TransferMetadata: transferMetadata},
		},
	}

	auditorService := newAuditorService(configuration, deserializer, auditTokens)
	if err := auditorService.AuditorCheck(context.Background(), tr, tokenRequestMetadata, "1"); err != nil {
		return nil, nil, nil, err
	}

	sigma, err := auditorEndorse(auditorSigner, tr, "1")
	if err != nil {
		return nil, nil, nil, err
	}
	araw, err := auditorSigner.Serialize()
	if err != nil {
		return nil, nil, nil, err
	}
	tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  araw,
			Signature: sigma,
		},
	})

	for range ids {
		signature, err := configuration.OwnerIdentity.Signer.Sign(raw)
		if err != nil {
			return nil, nil, nil, err
		}
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  0,
				Signature: signature,
			},
		})
	}

	// Add issuer signature for redeem case.
	snarkTransferAction, ok := transferAction.(*snarktoken.TransferAction)
	if !ok {
		return nil, nil, nil, errors.Errorf("expected *snarktoken.TransferAction, got [%T]", transferAction)
	}
	if len(snarkTransferAction.Issuer) > 0 {
		issuerSignature, err := configuration.IssuerSigner.Sign(raw)
		if err != nil {
			return nil, nil, nil, err
		}
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  0,
				Signature: issuerSignature,
			},
		})
	}

	return tr, tokenRequestMetadata, auditTokens, nil
}

// prepareTransferRequest builds a single-input, single-output transfer.
func prepareTransferRequest(configuration *SetupConfiguration, l *ledger, auditorSigner *Signer) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := [][]byte{configuration.OwnerIdentity.ID}

	return prepareTransferWithOpts(configuration, l, 1, 1, owners, auditorSigner, nil)
}

// prepareRedeemRequest builds a 2-input, 2-output transfer whose first
// output has a nil owner (a redeem/burn output).
func prepareRedeemRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := [][]byte{nil, configuration.OwnerIdentity.ID}

	return prepareTransferWithOpts(configuration, l, 2, 2, owners, configuration.AuditorSigner, nil)
}

// preparePublicMetadataTransferRequest builds a single-input, single-output
// transfer carrying a "pub."-prefixed application metadata attribute, so
// that TransferApplicationDataValidate's metadata-claiming branch is
// exercised end-to-end.
func preparePublicMetadataTransferRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := [][]byte{configuration.OwnerIdentity.ID}
	attrs := map[string]any{
		meta.TransferMetadataPrefix + meta.PublicMetadataPrefix + "note": []byte("public transfer metadata"),
	}

	return prepareTransferWithOpts(configuration, l, 1, 1, owners, configuration.AuditorSigner, attrs)
}

// prepareUnclaimedMetadataTransferRequest builds a single-input,
// single-output transfer carrying a metadata attribute whose key, once the
// meta.TransferMetadataPrefix is stripped, does NOT start with
// meta.PublicMetadataPrefix ("pub."). No validator branch claims such a key
// via ctx.CountMetadataKey, so VerifyTransfer's metadata-counter invariant
// ("more metadata than those validated") fails when the resulting request
// is re-validated. This is a NEGATIVE fixture: the auditor check still
// succeeds (AuditorCheck only checks structural correspondence, not
// metadata content), so the failure must be observed by re-running the real
// validator stack over the returned bytes.
func prepareUnclaimedMetadataTransferRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := [][]byte{configuration.OwnerIdentity.ID}
	attrs := map[string]any{
		meta.TransferMetadataPrefix + "unclaimed": []byte("unclaimed transfer metadata"),
	}

	return prepareTransferWithOpts(configuration, l, 1, 1, owners, configuration.AuditorSigner, attrs)
}

// prepareMultiAuditorTransferRequest builds a single-input, single-output
// transfer endorsed by the configuration's SecondAuditorSigner rather than
// the first-registered AuditorSigner. AuditingSignaturesValidate implements
// a 1-of-N policy over PP.Auditors(), so this exercises the "signed by a
// non-first configured auditor key" path end-to-end while remaining a
// POSITIVE fixture.
func prepareMultiAuditorTransferRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := [][]byte{configuration.OwnerIdentity.ID}

	return prepareTransferWithOpts(configuration, l, 1, 1, owners, configuration.SecondAuditorSigner, nil)
}

// prepareExtraSignatureTransferRequest builds a normal transfer and then
// appends one additional, spurious action signature beyond what
// TransferSignatureValidate will ever consume for that action. Backend.
// EnsureExhausted fails with "unconsumed signatures" once this request is
// re-validated. This is a NEGATIVE fixture: the auditor check still
// succeeds, since the auditor never inspects signature counts.
func prepareExtraSignatureTransferRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	tr, trMetadata, trInputs, err := prepareTransferRequest(configuration, l, configuration.AuditorSigner)
	if err != nil {
		return nil, nil, nil, err
	}

	tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
		Action: &driver.ActionSignature{
			ActionID:  0,
			Signature: []byte("spurious extra signature"),
		},
	})

	return tr, trMetadata, trInputs, nil
}

// prepareSwapRequest builds an atomic 2-action request: two independent
// single-input/single-output transfers combined into one TokenRequest,
// endorsed once by the auditor over both actions together.
func prepareSwapRequest(configuration *SetupConfiguration, l *ledger) (*driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	tr1, trMetadata1, inputs1, err := prepareTransferRequest(configuration, l, configuration.AuditorSigner)
	if err != nil {
		return nil, nil, nil, err
	}
	tr2, trMetadata2, inputs2, err := prepareTransferRequest(configuration, l, configuration.AuditorSigner)
	if err != nil {
		return nil, nil, nil, err
	}

	ar := &driver.TokenRequest{
		Actions: append(tr1.Actions, tr2.Actions...),
	}
	raw, err := ar.MarshalToMessageToSign([]byte("2"))
	if err != nil {
		return nil, nil, nil, err
	}

	metadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{ActionID: 0, TransferMetadata: trMetadata1.Actions[0].TransferMetadata},
			{ActionID: 1, TransferMetadata: trMetadata2.Actions[0].TransferMetadata},
		},
	}

	auditTokens := make(map[string]*token2.Token, len(inputs1)+len(inputs2))
	maps.Copy(auditTokens, inputs1)
	maps.Copy(auditTokens, inputs2)

	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create deserializer")
	}
	auditorService := newAuditorService(configuration, deserializer, auditTokens)
	if err := auditorService.AuditorCheck(context.Background(), ar, metadata, "2"); err != nil {
		return nil, nil, nil, err
	}

	sigma, err := auditorEndorse(configuration.AuditorSigner, ar, "2")
	if err != nil {
		return nil, nil, nil, err
	}
	araw, err := configuration.AuditorSigner.Serialize()
	if err != nil {
		return nil, nil, nil, err
	}
	ar.Signatures = append(ar.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  araw,
			Signature: sigma,
		},
	})

	// Each action's owner signs over the combined swap message, not the
	// single-action message computed inside prepareTransferRequest: the
	// signed bytes depend on the full set of actions in the request.
	for actionID := range 2 {
		signature, err := configuration.OwnerIdentity.Signer.Sign(raw)
		if err != nil {
			return nil, nil, nil, err
		}
		ar.Signatures = append(ar.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  uint32(actionID),
				Signature: signature,
			},
		})
	}

	return ar, metadata, auditTokens, nil
}

// auditorEndorse signs the marshalled token request with the given signer.
func auditorEndorse(signer *Signer, tokenRequest *driver.TokenRequest, txID string) ([]byte, error) {
	bytes, err := tokenRequest.MarshalToMessageToSign([]byte(txID))
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling token request [%s]", txID)
	}

	return signer.Sign(bytes)
}
