/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"
	"crypto"
	"encoding/json"
	"slices"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// UpgradeTokenType is the token type of the fabtoken outputs upgraded by the
// upgrade fixtures.
const UpgradeTokenType = "ABC"

// UpgradeFixture is a fabtoken-to-zkatsnark upgrade issuance built through
// the production IssueService (burn-and-re-issue) and AuditorService. Tests
// sign it with Sign, possibly after tampering with Action, and verify it with
// Validator.
type UpgradeFixture struct {
	// Configuration is the setup the fixture was built against.
	Configuration *SetupConfiguration
	// Validator is a validator over the configuration's public parameters.
	Validator *validator.Validator
	// Owners sign the redeemed fabtoken outputs; Owners[i] owns Tokens[i].
	Owners []*Signer
	// Tokens are the redeemed fabtoken outputs as recorded on the ledger.
	Tokens []token2.LedgerToken
	// Recipient receives the re-issued note.
	Recipient *Signer
	// Action is the upgrade issue action.
	Action *snarktoken.IssueAction
	// Metadata is the issue metadata produced by the issue service.
	Metadata *driver.IssueMetadata
	// AuditorService checks the request as the auditor would.
	AuditorService *zkatsnark.AuditorService
	// AuditTokens is the auditor's own copy of the redeemed tokens, keyed by
	// token ID string. AuditorService reads it on every check, so tests can
	// tamper with it.
	AuditTokens map[string]*token2.Token
	// IssueService is the issue service that built Action.
	IssueService *zkatsnark.IssueService
	// UpgradeRequest is the tokens upgrade request Action was built from.
	UpgradeRequest *driver.TokenUpgradeRequest
	// IssuerWallet is the issuer wallet Action was built with.
	IssuerWallet driver.IssuerWallet
}

// UpgradeOptions configures NewUpgradeFixtureWithOptions.
type UpgradeOptions struct {
	// Quantities lists the quantity (hex or decimal string) of each redeemed
	// fabtoken output.
	Quantities []string
	// SameOwner makes every redeemed output owned by the first owner.
	SameOwner bool
	// FabtokenPrecision is the precision of the fabtoken format of the
	// redeemed outputs. Zero means 64.
	FabtokenPrecision uint64
	// DriverWiring builds the tokens upgrade service exactly as the zkatsnark
	// driver does (zkatsnarkdriver.NewTokensServices), so that only the
	// fabtoken formats not spent in place are eligible. Otherwise every
	// fabtoken format is eligible, so that burn-and-re-issue can be exercised
	// under the default public parameters, where every format is spent in place.
	DriverWiring bool
}

// upgradeWalletService is a wallet service whose issuer wallet resolves the
// issuer identity for any token type, as the upgrade issuance requires.
type upgradeWalletService struct {
	*testWalletService
}

func (w *upgradeWalletService) IssuerWallet(ctx context.Context, id driver.WalletLookupID) (driver.IssuerWallet, error) {
	return &upgradeIssuerWallet{testIssuerWallet: &testIssuerWallet{signer: w.issuerSigner}}, nil
}

type upgradeIssuerWallet struct {
	*testIssuerWallet
}

func (w *upgradeIssuerWallet) GetIssuerIdentity(token2.Type) (driver.Identity, error) {
	return w.signer.Serialize()
}

// signerProvider resolves the signer of each upgrade owner.
type signerProvider map[string]*Signer

func (p signerProvider) GetSigner(_ context.Context, id driver.Identity) (driver.Signer, error) {
	s, ok := p[string(id)]
	if !ok {
		return nil, errors.Errorf("no signer for [%s]", id)
	}

	return s, nil
}

// NewUpgradeFixture builds an upgrade issuance redeeming one fabtoken output
// of the given quantity (hex or decimal string) per entry of quantities. The
// outputs are owned by fresh x509 owners; when sameOwner is true they all share
// the first owner. The re-issued note is assigned to a fresh x509 recipient.
func NewUpgradeFixture(configuration *SetupConfiguration, quantities []string, sameOwner bool) (*UpgradeFixture, error) {
	return NewUpgradeFixtureWithOptions(configuration, UpgradeOptions{Quantities: quantities, SameOwner: sameOwner})
}

// NewUpgradeFixtureWithOptions builds an upgrade issuance as configured by
// opts (see NewUpgradeFixture).
func NewUpgradeFixtureWithOptions(configuration *SetupConfiguration, opts UpgradeOptions) (*UpgradeFixture, error) {
	ctx := context.Background()
	quantities, sameOwner := opts.Quantities, opts.SameOwner
	precision := opts.FabtokenPrecision
	if precision == 0 {
		precision = 64
	}
	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create deserializer")
	}
	v, err := validator.NewValidator(configuration.Verifier, deserializer, resourceLimitsFor(configuration))
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}

	recipient, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	auditInfo := map[string][]byte{
		configuration.IssuerSigner.ID.String(): configuration.IssuerSigner.AuditInfo,
		recipient.ID.String():                  recipient.AuditInfo,
	}

	format, err := v1.SupportedTokenFormat(precision)
	if err != nil {
		return nil, err
	}
	f := &UpgradeFixture{Configuration: configuration, Validator: v, Recipient: recipient}
	signers := signerProvider{}
	for i, q := range quantities {
		var owner *Signer
		if sameOwner && i > 0 {
			owner = f.Owners[0]
		} else {
			owner, err = PrepareECDSASigner()
			if err != nil {
				return nil, err
			}
		}
		f.Owners = append(f.Owners, owner)
		signers[string(owner.ID)] = owner
		auditInfo[owner.ID.String()] = owner.AuditInfo

		raw, err := (&actions.Output{Owner: owner.ID, Type: UpgradeTokenType, Quantity: q}).Serialize()
		if err != nil {
			return nil, errors.Wrap(err, "failed to serialize fabtoken output")
		}
		f.Tokens = append(f.Tokens, token2.LedgerToken{
			ID:     token2.ID{TxId: "fabtoken-tx", Index: uint64(i)}, // #nosec G115
			Format: format,
			Token:  raw,
		})
	}

	ppm := &testPublicParamsManager{pp: configuration.PP}
	var upgradeService *snarktoken.TokensUpgradeService
	if opts.DriverWiring {
		_, upgradeService, err = zkatsnarkdriver.NewTokensServices(logging.MustGetLogger(), ppm, deserializer, signers)
	} else {
		upgradeService, err = snarktoken.NewTokensUpgradeService(
			logging.MustGetLogger(),
			configuration.PP.PrecisionValue,
			deserializer,
			signers,
			[]token2.Format{snarktoken.OutputTokenFormat},
		)
	}
	if err != nil {
		return nil, err
	}
	challenge, err := upgradeService.NewUpgradeChallenge()
	if err != nil {
		return nil, err
	}
	proof, err := upgradeService.GenUpgradeProof(ctx, challenge, f.Tokens, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate upgrade proof")
	}

	ws := &upgradeWalletService{testWalletService: &testWalletService{issuerSigner: configuration.IssuerSigner, auditInfoMap: auditInfo}}
	issueService := zkatsnark.NewIssueService(
		logging.MustGetLogger(),
		ppm,
		ws,
		deserializer,
		nil,
		upgradeService,
		configuration.Orchestrator,
		v,
	)
	f.IssueService = issueService
	f.UpgradeRequest = &driver.TokenUpgradeRequest{Challenge: challenge, Tokens: f.Tokens, Proof: proof}
	f.IssuerWallet = &upgradeIssuerWallet{testIssuerWallet: &testIssuerWallet{signer: configuration.IssuerSigner}}
	action, md, err := issueService.Issue(ctx, nil, "", nil, [][]byte{recipient.ID}, &driver.IssueOptions{
		TokensUpgradeRequest: f.UpgradeRequest,
		Wallet:               f.IssuerWallet,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to build upgrade issue")
	}
	ia, ok := action.(*snarktoken.IssueAction)
	if !ok {
		return nil, errors.Errorf("expected *snarktoken.IssueAction, got [%T]", action)
	}
	f.Action = ia
	f.Metadata = md

	f.AuditTokens = make(map[string]*token2.Token, len(f.Tokens))
	for i, tok := range f.Tokens {
		f.AuditTokens[tok.ID.String()] = &token2.Token{Owner: f.Owners[i].ID, Type: UpgradeTokenType, Quantity: quantities[i]}
	}
	f.AuditorService = newAuditorService(configuration, deserializer, f.AuditTokens)

	return f, nil
}

// Request serializes the fixture's action into a token request together with
// a copy of its metadata, which tests may tamper with without affecting the
// fixture. It is not signed.
func (f *UpgradeFixture) Request() (*driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	raw, err := f.Action.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize issue action")
	}
	issueMetadata := *f.Metadata
	issueMetadata.Inputs = slices.Clone(f.Metadata.Inputs)
	issueMetadata.Outputs = make([]*driver.IssueOutputMetadata, len(f.Metadata.Outputs))
	for i, out := range f.Metadata.Outputs {
		if out != nil {
			o := *out
			issueMetadata.Outputs[i] = &o
		}
	}
	tr := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}}}
	md := &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: &issueMetadata}}}

	return tr, md, nil
}

// Sign returns the token request for the fixture's action, endorsed by the
// auditor and signed by the issuer, followed by the passed extra signers in
// order (an honest upgrade issuance has none).
func (f *UpgradeFixture) Sign(txID string, extra ...*Signer) (*driver.TokenRequest, error) {
	tr, _, err := f.Request()
	if err != nil {
		return nil, err
	}
	msg, err := tr.MarshalToMessageToSign([]byte(txID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token request")
	}
	auditorSig, err := f.Configuration.AuditorSigner.Sign(msg)
	if err != nil {
		return nil, err
	}
	auditorID, err := f.Configuration.AuditorSigner.Serialize()
	if err != nil {
		return nil, err
	}
	tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{Identity: auditorID, Signature: auditorSig},
	})
	for _, s := range append([]*Signer{f.Configuration.IssuerSigner}, extra...) {
		sigma, err := s.Sign(msg)
		if err != nil {
			return nil, err
		}
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
			Action: &driver.ActionSignature{ActionID: 0, Signature: sigma},
		})
	}

	return tr, nil
}

// Verify runs the full validator pipeline on tr.
func (f *UpgradeFixture) Verify(txID string, tr *driver.TokenRequest) error {
	raw, err := tr.Bytes()
	if err != nil {
		return err
	}
	_, _, err = f.Validator.VerifyTokenRequestFromRaw(context.Background(), nil, driver.TokenRequestAnchor(txID), raw)

	return err
}

// FabtokenOutput serializes a fabtoken output, for tests that forge inputs.
func FabtokenOutput(owner driver.Identity, typ token2.Type, quantity string) ([]byte, error) {
	return (&actions.Output{Owner: owner, Type: typ, Quantity: quantity}).Serialize()
}

// ScriptIdentity returns an HTLC-script-typed identity, for tests that check
// script owners are refused.
func ScriptIdentity() (driver.Identity, error) {
	return identity.WrapWithType(driver.HTLCScriptIdentityType, []byte("script"))
}

// InPlaceFixture is a zkatsnark transfer that spends legacy fabtoken outputs
// directly, upgrading them in place, built through the production
// TransferService and AuditorService.
type InPlaceFixture struct {
	// Configuration is the setup the fixture was built against.
	Configuration *SetupConfiguration
	// Validator is a validator over the configuration's public parameters.
	Validator *validator.Validator
	// Owner owns every spent fabtoken output (for an HTLC fixture, it is the
	// script recipient).
	Owner *Signer
	// InputSigner signs the transfer once per input on behalf of the owner of
	// the spent fabtoken outputs.
	InputSigner driver.Signer
	// TransferService is the transfer service that built Action.
	TransferService *zkatsnark.TransferService
	// IDs are the IDs of the spent fabtoken outputs, in input order.
	IDs []*token2.ID
	// Tokens are the spent fabtoken outputs; Ledger serves them by ID.
	Tokens []token2.LedgerToken
	// Ledger is the ledger the validator reads the inputs from.
	Ledger *ledger
	// Action is the transfer action.
	Action *snarktoken.TransferAction
	// Metadata is the transfer metadata produced by the transfer service.
	Metadata *driver.TransferMetadata
	// AuditorService checks the request as the auditor would.
	AuditorService *zkatsnark.AuditorService
}

// NewInPlaceFixture builds a transfer spending one fabtoken output of type
// UpgradeTokenType per entry of quantities, all owned by a fresh x509 owner,
// into a single zkatsnark output for a fresh recipient carrying their total.
func NewInPlaceFixture(configuration *SetupConfiguration, quantities []string) (*InPlaceFixture, error) {
	owner, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	recipient, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	auditInfo := map[string][]byte{owner.ID.String(): owner.AuditInfo, recipient.ID.String(): recipient.AuditInfo}

	return newInPlaceFixture(configuration, quantities, owner.ID, owner, recipient.ID, auditInfo, nil, owner)
}

// NewHTLCInPlaceFixture builds a transfer that claims one fabtoken output of
// the given quantity locked in an HTLC script: the output is owned by the
// script, whose recipient claims it, in place, into a zkatsnark note, by
// revealing the preimage. The returned fixture's Owner is the script recipient.
func NewHTLCInPlaceFixture(configuration *SetupConfiguration, quantity string) (*InPlaceFixture, error) {
	sender, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	recipient, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	preimage := []byte("zkatsnark-htlc-in-place-preimage")
	hashInfo := htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.None}
	image, err := hashInfo.Image(preimage)
	if err != nil {
		return nil, err
	}
	hashInfo.Hash = image
	script := &htlc.Script{Sender: sender.ID, Recipient: recipient.ID, Deadline: time.Now().Add(time.Hour), HashInfo: hashInfo}
	rawScript, err := json.Marshal(script)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal htlc script")
	}
	scriptID, err := identity.WrapWithType(htlc.ScriptType, rawScript)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wrap htlc script identity")
	}
	auditInfo := map[string][]byte{sender.ID.String(): sender.AuditInfo, recipient.ID.String(): recipient.AuditInfo}
	attrs := map[string]any{meta.TransferMetadataPrefix + htlc.ClaimKey(image): preimage}
	signer := &htlc.ClaimSigner{Recipient: recipient, Preimage: preimage}

	return newInPlaceFixture(configuration, []string{quantity}, scriptID, signer, recipient.ID, auditInfo, attrs, recipient)
}

// newInPlaceFixture builds a transfer spending one fabtoken output of type
// UpgradeTokenType per entry of quantities, all owned by ownerID, into a
// single zkatsnark output for recipientID carrying their total. signer signs
// the request once per input; owner is reported as the fixture's Owner.
func newInPlaceFixture(
	configuration *SetupConfiguration,
	quantities []string,
	ownerID driver.Identity,
	signer driver.Signer,
	recipientID driver.Identity,
	auditInfo map[string][]byte,
	attrs map[string]any,
	owner *Signer,
) (*InPlaceFixture, error) {
	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create deserializer")
	}
	v, err := validator.NewValidator(configuration.Verifier, deserializer, resourceLimitsFor(configuration))
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}
	format, err := v1.SupportedTokenFormat(64)
	if err != nil {
		return nil, err
	}

	f := &InPlaceFixture{Configuration: configuration, Validator: v, Owner: owner, InputSigner: signer, Ledger: newLedger()}
	loaded := make(map[string]common.LoadedToken[[]byte, []byte], len(quantities))
	audited := make(map[string]*token2.Token, len(quantities))
	ids := make([]*token2.ID, len(quantities))
	var total uint64
	for i, q := range quantities {
		raw, err := FabtokenOutput(ownerID, UpgradeTokenType, q)
		if err != nil {
			return nil, err
		}
		id := token2.ID{TxId: "fabtoken-tx", Index: uint64(i)} // #nosec G115
		ids[i] = &id
		f.Tokens = append(f.Tokens, token2.LedgerToken{ID: id, Format: format, Token: raw})
		f.Ledger.store(&id, raw)
		loaded[id.String()] = common.LoadedToken[[]byte, []byte]{TokenFormat: format, Token: raw}
		audited[id.String()] = &token2.Token{Owner: ownerID, Type: UpgradeTokenType, Quantity: q}
		parsed, err := snarktoken.ParseFabtokenOutput(raw, configuration.PP.PrecisionValue)
		if err != nil {
			return nil, err
		}
		total += parsed.Value
	}

	ts, err := newTransferService(configuration, deserializer, &testTokenLoader{tokens: loaded}, auditInfo)
	if err != nil {
		return nil, err
	}
	f.TransferService = ts
	f.IDs = ids
	action, md, err := ts.Transfer(context.Background(), "tx", &testOwnerWallet{id: "owner", signer: owner}, ids, []*token2.Token{{
		Owner:    recipientID,
		Type:     UpgradeTokenType,
		Quantity: token2.NewQuantityFromUInt64(total).Hex(),
	}}, &driver.TransferOptions{Attributes: attrs})
	if err != nil {
		return nil, errors.Wrap(err, "failed to build in-place upgrade transfer")
	}
	ta, ok := action.(*snarktoken.TransferAction)
	if !ok {
		return nil, errors.Errorf("expected *snarktoken.TransferAction, got [%T]", action)
	}
	f.Action = ta
	f.Metadata = md
	f.AuditorService = newAuditorService(configuration, deserializer, audited)

	return f, nil
}

// Request serializes the fixture's action into a token request together with
// its metadata. It is not signed.
func (f *InPlaceFixture) Request() (*driver.TokenRequest, *driver.TokenRequestMetadata, error) {
	raw, err := f.Action.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize transfer action")
	}
	tr := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}}
	md := &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: f.Metadata}}}

	return tr, md, nil
}

// SignAndVerify signs the fixture's request with the auditor and, once per
// input, the input signer, and runs the full validator pipeline on it.
func (f *InPlaceFixture) SignAndVerify(txID string) error {
	tr, _, err := f.Request()
	if err != nil {
		return err
	}
	msg, err := tr.MarshalToMessageToSign([]byte(txID))
	if err != nil {
		return err
	}
	auditorSig, err := f.Configuration.AuditorSigner.Sign(msg)
	if err != nil {
		return err
	}
	auditorID, err := f.Configuration.AuditorSigner.Serialize()
	if err != nil {
		return err
	}
	tr.Signatures = append(tr.Signatures, &driver.RequestSignature{Auditor: &driver.AuditorSignature{Identity: auditorID, Signature: auditorSig}})
	for range f.Action.Inputs {
		sigma, err := f.InputSigner.Sign(msg)
		if err != nil {
			return err
		}
		tr.Signatures = append(tr.Signatures, &driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: sigma}})
	}
	raw, err := tr.Bytes()
	if err != nil {
		return err
	}
	_, _, err = f.Validator.VerifyTokenRequestFromRaw(context.Background(), f.Ledger.GetState, driver.TokenRequestAnchor(txID), raw)

	return err
}
