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

	math "github.com/IBM/mathlib"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	zkatdlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	issue2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	tokn "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace/noop"
)

type Env struct {
	Engine *validator.Validator

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

	Sender             *transfer.Sender
	TRWithSwap         *driver.TokenRequest
	TRWithSwapTxID     string
	TRWithSwapRaw      []byte
	TRWithSwapMetadata *driver.TokenRequestMetadata
	TRWithSwapInputs   map[string]*token2.Token
}

func NewEnv(benchCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*Env, error) {
	var (
		engine *validator.Validator

		sender  *transfer.Sender
		auditor *audit.Auditor

		ir         *driver.TokenRequest         // regular issue request
		irMetadata *driver.TokenRequestMetadata // issue metadata
		irInputs   map[string]*token2.Token     // issue inputs (empty for issues)
		rr         *driver.TokenRequest         // redeem request
		rrMetadata *driver.TokenRequestMetadata // redeem metadata
		rrInputs   map[string]*token2.Token     // redeem inputs
		tr         *driver.TokenRequest         // transfer request
		trMetadata *driver.TokenRequestMetadata // transfer metadata
		trInputs   map[string]*token2.Token     // transfer inputs
		ar         *driver.TokenRequest         // atomic action request
		arMetadata *driver.TokenRequestMetadata // swap metadata
		arInputs   map[string]*token2.Token     // swap inputs
	)

	// prepare public parameters
	setupConfiguration, err := configurations.GetSetupConfiguration(benchCase.Bits, benchCase.CurveID)
	if err != nil {
		return nil, err
	}
	pp := setupConfiguration.PP
	oID := setupConfiguration.OwnerIdentity

	c := math.Curves[pp.Curve]

	deserializer, err := zkatdlog.NewDeserializer(pp)
	if err != nil {
		return nil, err
	}
	auditor = audit.NewAuditor(logging.MustGetLogger(), &noop.Tracer{}, deserializer, pp.PedersenGenerators, c, 64, pp.IssuerIDs)

	engine = validator.New(
		logging.MustGetLogger(),
		pp,
		deserializer,
		driver.DefaultResourceLimits(),
		nil,
		nil,
		nil,
	)

	// non-anonymous issue
	_, ir, irMetadata, err = prepareIssueRequest(pp, auditor, setupConfiguration)
	if err != nil {
		return nil, err
	}
	irInputs = map[string]*token2.Token{}
	irRaw, err := ir.Bytes()
	if err != nil {
		return nil, err
	}

	// prepare redeem
	_, rr, rrMetadata, rrInputs, err = prepareRedeemRequest(benchCase, pp, auditor, setupConfiguration)
	if err != nil {
		return nil, err
	}
	rrRaw, err := rr.Bytes()
	if err != nil {
		return nil, err
	}

	// prepare transfer
	_, tr, trMetadata, trInputs, err = prepareTransferRequest(benchCase, pp, auditor, setupConfiguration.AuditorSigner, oID)
	if err != nil {
		return nil, err
	}
	transferRaw, err := tr.Bytes()
	if err != nil {
		return nil, err
	}

	// atomic action request
	sender, ar, arMetadata, arInputs, err = prepareSwapRequest(benchCase, pp, auditor, setupConfiguration.AuditorSigner, oID)
	if err != nil {
		return nil, err
	}
	// arInputs is already [][]*tokn.Token from prepareSwapRequest
	arRaw, err := ar.Bytes()
	if err != nil {
		return nil, err
	}

	return &Env{
		Engine: engine,
		Sender: sender,

		TRWithTransferTxID:     "1",
		TRWithTransfer:         tr,
		TRWithTransferRaw:      transferRaw,
		TRWithTransferMetadata: trMetadata,
		TRWithTransferInputs:   trInputs,

		TRWithRedeem:         rr,
		TRWithRedeemTxID:     "1",
		TRWithRedeemRaw:      rrRaw,
		TRWithRedeemMetadata: rrMetadata,
		TRWithRedeemInputs:   rrInputs,

		TRWithIssue:         ir,
		TRWithIssueTxID:     "1",
		TRWithIssueRaw:      irRaw,
		TRWithIssueMetadata: irMetadata,
		TRWithIssueInputs:   irInputs,

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

// TestCase represents a single test case with all its data
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

// TransferToTestCase converts the Env's transfer data to a TestCase
func (e *Env) TransferToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithTransferTxID, e.TRWithTransferRaw, e.TRWithTransferMetadata, e.TRWithTransferInputs)
}

// IssueToTestCase converts the Env's issue data to a TestCase
func (e *Env) IssueToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithIssueTxID, e.TRWithIssueRaw, e.TRWithIssueMetadata, e.TRWithIssueInputs)
}

// RedeemToTestCase converts the Env's redeem data to a TestCase
func (e *Env) RedeemToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithRedeemTxID, e.TRWithRedeemRaw, e.TRWithRedeemMetadata, e.TRWithRedeemInputs)
}

// SwapToTestCase converts the Env's swap data to a TestCase
func (e *Env) SwapToTestCase() (*TestCase, error) {
	return e.toTestCase(e.TRWithSwapTxID, e.TRWithSwapRaw, e.TRWithSwapMetadata, e.TRWithSwapInputs)
}

func (e *Env) toTestCase(txID string, raw []byte, metadata *driver.TokenRequestMetadata, inputs map[string]*token2.Token) (*TestCase, error) {
	if e == nil {
		return nil, errors.Errorf("nil Env")
	}

	tc := &TestCase{
		TxID:   txID,
		ReqRaw: base64.StdEncoding.EncodeToString(raw),
	}

	// Serialize metadata if present
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

	// Serialize metadata
	var metadataEncoded string
	if metadata != nil {
		metadataBytes, err := metadata.Bytes()
		if err != nil {
			return errors.Wrap(err, "failed to marshal metadata")
		}
		metadataEncoded = base64.StdEncoding.EncodeToString(metadataBytes)
	}

	// Serialize inputs
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

func prepareIssueRequest(pp *v1setup.PublicParams, auditor *audit.Auditor, setupConfiguration *benchmark.SetupConfiguration) (*issue2.Issuer, *driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	// Create PublicParametersManager
	ppm := &testPublicParamsManager{pp: pp}

	// Create deserializer
	deserializer, err := zkatdlog.NewDeserializer(pp)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create deserializer")
	}

	// Create WalletService
	ws := &testWalletService{
		issuerSigner: setupConfiguration.IssuerSigner,
		auditInfoMap: map[string][]byte{
			setupConfiguration.IssuerSigner.ID.String():  setupConfiguration.IssuerSigner.AuditInfo,
			setupConfiguration.OwnerIdentity.ID.String(): setupConfiguration.OwnerIdentity.AuditInfo,
		},
	}

	// Create IdentityProvider - pass the owner's signer directly
	ip := &testIdentityProvider{ownerSigner: setupConfiguration.OwnerIdentity.Signer}

	// Create TokensService
	tokensService, err := tokn.NewTokensService(logging.MustGetLogger(), ppm, deserializer)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create tokens service")
	}

	// Create TokensUpgradeService
	tokensUpgradeService, err := upgrade.NewService(logging.MustGetLogger(), pp.QuantityPrecision, deserializer, ip)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create tokens upgrade service")
	}

	// Create IssueService - this is the production stack instantiation
	issueService := v1.NewIssueService(
		logging.MustGetLogger(),
		ppm,
		ws,
		deserializer,
		tokensService,
		tokensUpgradeService,
	)

	// Get issuer identity
	issuerIdentity, err := setupConfiguration.IssuerSigner.Serialize()
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to serialize issuer identity")
	}

	// Use IssueService to create the issue action
	owners := [][]byte{setupConfiguration.OwnerIdentity.ID}
	values := []uint64{40}

	issueAction, issueMetadata, err := issueService.Issue(
		context.Background(),
		issuerIdentity,
		"ABC",
		values,
		owners,
		nil, // no options
	)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to issue tokens")
	}

	// Serialize the issue action
	raw, err := issueAction.Serialize()
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to serialize issue action")
	}

	// Create token request
	ir := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw},
		},
	}

	// Marshal to sign
	rawToSign, err := ir.MarshalToMessageToSign([]byte("1"))
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to marshal token request")
	}

	// Create issuer for signing (still needed for backward compatibility)
	issuer := issue2.NewIssuer("ABC", setupConfiguration.IssuerSigner, pp)

	// Sign with issuer
	sig, err := issuer.SignTokenActions(rawToSign)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to sign token actions")
	}

	// Create request metadata
	requestMetadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{ActionID: 0, IssueMetadata: issueMetadata},
		},
	}

	// Auditor check - issues have no inputs, so pass empty map
	err = auditor.Check(context.Background(), ir, requestMetadata, "1", map[string]*token2.Token{})
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "auditor check failed")
	}

	// Auditor endorsement
	sigma, err := auditorEndorse(setupConfiguration.AuditorSigner, ir, "1")
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to get auditor endorsement")
	}

	araw, err := setupConfiguration.AuditorSigner.Serialize()
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to serialize auditor identity")
	}

	// Add signatures
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

	return issuer, ir, requestMetadata, nil
}

func prepareRedeemRequest(benchCase *benchmark2.Case, pp *v1setup.PublicParams, auditor *audit.Auditor, setupConfig *benchmark.SetupConfiguration) (*transfer.Sender, *driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	benchCaseRedeem := &benchmark2.Case{
		Workers:    benchCase.Workers,
		Bits:       benchCase.Bits,
		CurveID:    benchCase.CurveID,
		NumInputs:  benchCase.NumInputs,
		NumOutputs: 2,
	}
	owners := make([][]byte, 2)
	for i := range benchCase.NumInputs {
		owners[i] = setupConfig.OwnerIdentity.ID
	}
	owners[0] = nil

	issuer := issue2.NewIssuer("ABC", setupConfig.IssuerSigner, pp)
	issuerIdentity, err := setupConfig.IssuerSigner.Serialize()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return prepareTransfer(
		benchCaseRedeem,
		pp,
		setupConfig.OwnerIdentity.Signer,
		auditor,
		setupConfig.OwnerIdentity.AuditInfo,
		setupConfig.OwnerIdentity.ID,
		owners,
		issuer,
		issuerIdentity,
		setupConfig.AuditorSigner,
	)
}

func prepareTransferRequest(benchCase *benchmark2.Case, pp *v1setup.PublicParams, auditor *audit.Auditor, signer *benchmark.Signer, oID *benchmark.OwnerIdentity) (*transfer.Sender, *driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	owners := make([][]byte, benchCase.NumOutputs)
	for i := range benchCase.NumOutputs {
		owners[i] = oID.ID
	}

	return prepareTransfer(
		benchCase,
		pp,
		oID.Signer,
		auditor,
		oID.AuditInfo,
		oID.ID,
		owners,
		nil,
		nil,
		signer,
	)
}

func prepareSwapRequest(benchCase *benchmark2.Case, pp *v1setup.PublicParams, auditor *audit.Auditor, auditorSigner *benchmark.Signer, oID *benchmark.OwnerIdentity) (*transfer.Sender, *driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	sender1, tr1, trmetadata1, inputsForTransfer1, err := prepareTransferRequest(benchCase, pp, auditor, auditorSigner, oID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sender2, tr2, trmetadata2, inputsForTransfer2, err := prepareTransferRequest(benchCase, pp, auditor, auditorSigner, oID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	//
	ar := &driver.TokenRequest{
		Actions: append(tr1.Actions, tr2.Actions...),
	}
	raw, err := ar.MarshalToMessageToSign([]byte("2"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Sender signs request
	sender1Signatures, err := sender1.SignTokenActions(raw)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sender2Signatures, err := sender2.SignTokenActions(raw)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// auditor inspect token
	metadata := &driver.TokenRequestMetadata{}
	metadata.Actions = []*driver.ActionMetadataEntry{
		{ActionID: 0, TransferMetadata: trmetadata1.Actions[0].TransferMetadata},
		{ActionID: 1, TransferMetadata: trmetadata2.Actions[0].TransferMetadata},
	}

	auditTokens := make(map[string]*token2.Token)
	maps.Copy(auditTokens, inputsForTransfer1)
	maps.Copy(auditTokens, inputsForTransfer2)

	err = auditor.Check(context.Background(), ar, metadata, "2", auditTokens)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sigma, err := auditorEndorse(auditorSigner, ar, "2")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ar.Signatures = append(ar.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  pp.Auditors()[0],
			Signature: sigma,
		},
	})

	for _, signature := range sender1Signatures {
		ar.Signatures = append(ar.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  0,
				Signature: signature,
			},
		})
	}
	for _, signature := range sender2Signatures {
		ar.Signatures = append(ar.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  1,
				Signature: signature,
			},
		})
	}

	return sender1, ar, metadata, auditTokens, nil
}

func prepareTokens(values, bf []*math.Zr, tokenType string, pp []*math.G1, curve *math.Curve) []*math.G1 {
	tokens := make([]*math.G1, len(values))
	for i := range values {
		tokens[i] = prepareToken(values[i], bf[i], tokenType, pp, curve)
	}

	return tokens
}

func prepareToken(value *math.Zr, rand *math.Zr, tokenType string, pp []*math.G1, curve *math.Curve) *math.G1 {
	token := curve.NewG1()
	token.Add(pp[0].Mul(curve.HashToZr([]byte(tokenType))))
	token.Add(pp[1].Mul(value))
	token.Add(pp[2].Mul(rand))

	return token
}

func prepareTransfer(
	benchCase *benchmark2.Case,
	pp *v1setup.PublicParams,
	signer driver.SigningIdentity,
	auditor *audit.Auditor,
	auditInfo []byte,
	id []byte,
	owners [][]byte,
	issuer *issue2.Issuer,
	issuerIdentity []byte,
	auditorSigner *benchmark.Signer,
) (*transfer.Sender, *driver.TokenRequest, *driver.TokenRequestMetadata, map[string]*token2.Token, error) {
	signers := make([]driver.Signer, benchCase.NumInputs)
	for i := range benchCase.NumInputs {
		signers[i] = signer
	}
	c := math.Curves[pp.Curve]

	// prepare inputs
	inValues := make([]*math.Zr, benchCase.NumInputs)
	inValuesUint64 := make([]uint64, benchCase.NumInputs)
	sumInputs := uint64(0)
	for i := range inValues {
		v := uint64(i*10 + 500)
		sumInputs += v
		inValuesUint64[i] = v
		inValues[i] = math2.NewCachedZrFromInt(c, v)
	}

	if benchCase.NumOutputs <= 0 {
		return nil, nil, nil, nil, errors.Errorf("invalid number of outputs [%d]", benchCase.NumOutputs)
	}
	outputValue := sumInputs / uint64(benchCase.NumOutputs)
	sumOutputs := uint64(0)
	outValues := make([]uint64, benchCase.NumOutputs)
	for i := range benchCase.NumOutputs {
		outValues[i] = outputValue
		sumOutputs += outputValue
	}
	// add any adjustment to the last output
	delta := sumInputs - sumOutputs
	if delta > 0 {
		outValues[0] += delta
	}

	inBF := make([]*math.Zr, benchCase.NumInputs)
	rand, err := c.Rand()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for i := range benchCase.NumInputs {
		inBF[i] = c.NewRandomZr(rand)
	}

	ids := make([]*token2.ID, benchCase.NumInputs)
	for i := range benchCase.NumInputs {
		ids[i] = &token2.ID{TxId: "0", Index: uint64(i)}
	}
	inputs := prepareTokens(inValues, inBF, "ABC", pp.PedersenGenerators, c)

	tokens := make([]*tokn.Token, benchCase.NumInputs)
	inputInf := make([]*tokn.Metadata, benchCase.NumInputs)
	for i := range benchCase.NumInputs {
		tokens[i] = &tokn.Token{Data: inputs[i], Owner: id}
		inputInf[i] = &tokn.Metadata{Type: "ABC", Value: inValues[i], BlindingFactor: inBF[i]}
	}

	// Create PublicParametersManager
	ppm := &testPublicParamsManager{pp: pp}

	// Create deserializer
	deserializer, err := zkatdlog.NewDeserializer(pp)
	if err != nil {
		return nil, nil, nil, nil, errors.Wrap(err, "failed to create deserializer")
	}

	// Create TokensService first to get the proper token format
	tokensService, err := tokn.NewTokensService(
		logging.MustGetLogger(),
		ppm,
		deserializer,
	)
	if err != nil {
		return nil, nil, nil, nil, errors.Wrap(err, "failed to create tokens service")
	}

	// Get the proper token format from TokensService
	tokenFormat := tokensService.OutputTokenFormat

	// Prepare token loader with the input tokens
	tokenLoaderMap := make(map[string]v1.LoadedToken)
	for i, tok := range tokens {
		tokenRaw, err := tok.Serialize()
		if err != nil {
			return nil, nil, nil, nil, errors.Wrap(err, "failed to serialize token for loader")
		}
		metadataRaw, err := inputInf[i].Serialize()
		if err != nil {
			return nil, nil, nil, nil, errors.Wrap(err, "failed to serialize metadata for loader")
		}
		key := ids[i].String()
		tokenLoaderMap[key] = v1.LoadedToken{
			Token:       tokenRaw,
			Metadata:    metadataRaw,
			TokenFormat: tokenFormat,
		}
	}
	tokenLoader := &testTokenLoader{tokens: tokenLoaderMap}

	// Create WalletService with audit info
	// Add audit info for all token owners (inputs and outputs)
	auditInfoMap := make(map[string][]byte)
	for _, tok := range tokens {
		auditInfoMap[string(tok.Owner)] = auditInfo
	}
	// Also add audit info for output owners
	for _, owner := range owners {
		auditInfoMap[string(owner)] = auditInfo
	}
	ws := &testWalletService{
		auditInfoMap: auditInfoMap,
	}

	// Create TransferService - this is the production stack instantiation
	transferService := v1.NewTransferService(
		logging.MustGetLogger(),
		ppm,
		ws,
		tokenLoader,
		deserializer,
		noop.NewTracerProvider(),
		tokensService,
	)

	// Prepare output tokens in the format expected by TransferService.Transfer()
	outputTokens := make([]*token2.Token, benchCase.NumOutputs)
	for i := range benchCase.NumOutputs {
		outputTokens[i] = &token2.Token{
			Type:     "ABC",
			Quantity: token2.NewQuantityFromUInt64(outValues[i]).Hex(),
			Owner:    owners[i],
		}
	}

	// Create a mock OwnerWallet
	ownerWallet := &testOwnerWallet{
		id:     "test-owner-wallet",
		signer: signer,
	}

	// Use TransferService to create the transfer action
	// Pass empty options instead of nil to avoid nil pointer dereference in SelectIssuerForRedeem
	transfer2, transferMetadata, err := transferService.Transfer(
		context.Background(),
		"1", // anchor (txID)
		ownerWallet,
		ids,
		outputTokens,
		&driver.TransferOptions{}, // empty options
	)
	if err != nil {
		return nil, nil, nil, nil, errors.Wrap(err, "failed to generate transfer using TransferService")
	}

	// Handle issuer for redeem case
	if issuerIdentity != nil {
		// Cast to concrete type to set issuer
		if transferAction, ok := transfer2.(*transfer.Action); ok {
			transferAction.Issuer = issuerIdentity
		}
		transferMetadata.Issuer = driver.AuditableIdentity{
			Identity: issuerIdentity,
		}
	}

	// Serialize the transfer action
	transferRaw, err := transfer2.Serialize()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	tr := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
		},
	}
	raw, err := tr.MarshalToMessageToSign([]byte("1"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Create sender for backward compatibility (still needed for signing)
	sender, err := transfer.NewSender(signers, tokens, ids, inputInf, pp)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	tokenRequestMetadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{ActionID: 0, TransferMetadata: transferMetadata},
		},
	}

	// Build auditTokens map from input tokens
	auditTokens := make(map[string]*token2.Token)
	for i, tok := range tokens {
		auditTokens[ids[i].String()] = &token2.Token{
			Type:     "ABC",
			Quantity: token2.NewQuantityFromUInt64(inValuesUint64[i]).Hex(),
			Owner:    tok.Owner,
		}
	}

	err = auditor.Check(context.Background(), tr, tokenRequestMetadata, "1", auditTokens)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	sigma, err := auditorEndorse(auditorSigner, tr, "1")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	araw, err := auditorSigner.Serialize()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  araw,
			Signature: sigma,
		},
	})

	signatures, err := sender.SignTokenActions(raw)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, signature := range signatures {
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  0,
				Signature: signature,
			},
		})
	}

	// Add issuer signature for redeem case
	if issuer != nil {
		issuerSignature, err := issuer.Signer.Sign(raw)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{
				ActionID:  0,
				Signature: issuerSignature,
			},
		})
	}

	return sender, tr, tokenRequestMetadata, auditTokens, nil
}

func auditorEndorse(signer driver.Signer, tokenRequest *driver.TokenRequest, txID string) ([]byte, error) {
	// Marshal tokenRequest
	bytes, err := tokenRequest.MarshalToMessageToSign([]byte(txID))
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling token request [%s]", txID)
	}
	// Sign
	return signer.Sign(bytes)
}
