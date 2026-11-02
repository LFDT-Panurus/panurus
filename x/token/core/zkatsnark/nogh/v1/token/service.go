/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"bytes"
	"context"
	"slices"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	dlogmath "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// OutputTokenFormat is the format of every output the zkatsnark driver creates.
const OutputTokenFormat token.Format = "zkatsnark"

type TokensService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	Deserializer            driver.Deserializer
	// SupportedTokenFormatList lists the formats spendable directly: zkatsnark
	// outputs and, upgraded in place, the fabtoken formats of InPlaceFabtokenPrecisions.
	// ZKAT-DLOG formats are never listed: those outputs are only upgraded
	// through the issuer (TokensUpgradeService).
	SupportedTokenFormatList []token.Format
	// Legacy are the ZKAT-DLOG public parameters whose outputs can be
	// upgraded, used to open them (Deobfuscate). Nil when ZKAT-DLOG upgrades
	// are not supported.
	Legacy *pp.LegacyDLog
}

func NewTokensService(logger logging.Logger, publicParametersManager common.PublicParametersManager[*pp.PublicParams], deserializer driver.Deserializer) (*TokensService, error) {
	var p *pp.PublicParams
	if publicParametersManager != nil {
		p = publicParametersManager.PublicParams()
	}
	formats := []token.Format{OutputTokenFormat}
	for _, precision := range InPlaceFabtokenPrecisions(p) {
		format, err := fabtoken.SupportedTokenFormat(precision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed computing fabtoken token format with precision [%d]", precision)
		}
		formats = append(formats, format)
	}

	var legacyDLog *pp.LegacyDLog
	if p != nil {
		var err error
		legacyDLog, err = p.LegacyDLog()
		if err != nil {
			if !errors.Is(err, pp.ErrNoLegacyDLog) {
				return nil, errors.WithMessagef(err, "invalid ZKAT-DLOG public parameters")
			}
			legacyDLog = nil
		}
	}

	return &TokensService{
		Logger:                   logger,
		PublicParametersManager:  publicParametersManager,
		Deserializer:             deserializer,
		SupportedTokenFormatList: formats,
		Legacy:                   legacyDLog,
	}, nil
}

// InPlaceFabtokenPrecisions returns the fabtoken precisions that zkatsnark
// spends directly, upgrading them in place inside a transfer: those whose
// every value is a valid zkatsnark value, that is, at most both the quantity
// precision and the bit width of the circuits' range check. Fabtoken outputs
// of a higher precision are upgraded by burn-and-re-issue instead
// (TokensUpgradeService), which handles exactly the complement.
func InPlaceFabtokenPrecisions(p *pp.PublicParams) []uint64 {
	if p == nil {
		return nil
	}
	limit := p.PrecisionValue
	if p.MaxBits > 0 && uint64(p.MaxBits) < limit { // #nosec G115 -- MaxBits is positive
		limit = uint64(p.MaxBits)
	}
	var res []uint64
	for _, precision := range fabtokenPrecisions {
		if precision <= limit {
			res = append(res, precision)
		}
	}

	return res
}

func (s *TokensService) SupportedTokenFormats() []token.Format {
	return s.SupportedTokenFormatList
}

func (s *TokensService) Deobfuscate(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	if IsFabtokenOutput(output) {
		return s.deobfuscateFabtoken(output, outputMetadata)
	}
	if IsDLogOutput(output) {
		return s.deobfuscateDLog(output, outputMetadata)
	}
	note, err := Deserialize(outputMetadata)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to deserialize token metadata")
	}

	outputDesc, err := DeserializeOutputDescription(output)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal output")
	}

	curve := ecc.BLS12_381
	if s.PublicParametersManager != nil && s.PublicParametersManager.PublicParams() != nil {
		curve = s.PublicParametersManager.PublicParams().Curve
	}
	cm, err := note.Commitment(curve)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to compute commitment")
	}

	if !bytes.Equal(cm, outputDesc.CommitmentOut) {
		return nil, nil, nil, "", errors.New("metadata commitment does not match output commitment")
	}

	recipients, err := s.Deserializer.Recipients(outputDesc.Recipient)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to get recipients")
	}

	clearToken := &token.Token{
		Type:     token.Type(note.TokenType),
		Quantity: "0x" + strconv.FormatUint(note.Value, 16),
		Owner:    outputDesc.Recipient,
	}

	return clearToken, note.Issuer, recipients, OutputTokenFormat, nil
}

// deobfuscateFabtoken opens a legacy fabtoken output, which is in the clear.
//
// As zkatdlog's deobfuscateAsFabtokenType, it requires metadata it can parse:
// the fabtoken driver always stores an actions.OutputMetadata alongside each
// output (with the issuer for an issued output, empty for a transferred one),
// and the vault and token-store flows that reach here (spendability checks)
// pass it back unchanged. Outputs of zkatsnark requests are never fabtoken, so
// no flow needs to accept empty metadata.
func (s *TokensService) deobfuscateFabtoken(output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	out := &actions.Output{}
	if err := out.Deserialize(output); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal fabtoken output")
	}
	md := &actions.OutputMetadata{}
	if err := md.Deserialize(outputMetadata); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal fabtoken output metadata")
	}
	recipients, err := s.Deserializer.Recipients(out.Owner)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to get recipients")
	}

	return &token.Token{Owner: out.Owner, Type: out.Type, Quantity: out.Quantity}, md.Issuer, recipients, OutputTokenFormat, nil
}

// deobfuscateDLog opens a not-yet-upgraded ZKAT-DLOG output with the embedded
// ZKAT-DLOG public parameters, as ZKAT-DLOG itself does, so that wallets, the
// token store and auditors keep handling it until it is upgraded. It returns
// the ZKAT-DLOG output format, which zkatsnark never spends directly.
func (s *TokensService) deobfuscateDLog(output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	if s.Legacy == nil {
		return nil, nil, nil, "", errors.New("ZKAT-DLOG output, but no ZKAT-DLOG public parameters are configured")
	}
	out := &dlogtoken.Token{}
	if err := out.Deserialize(output); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal ZKAT-DLOG output")
	}
	if err := dlogmath.CheckElement(out.Data, s.Legacy.PP.Curve); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "invalid ZKAT-DLOG commitment")
	}
	md := &dlogtoken.Metadata{}
	if err := md.Deserialize(outputMetadata); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal ZKAT-DLOG output metadata")
	}
	if err := dlogmath.CheckBaseElement(md.Value, s.Legacy.PP.Curve); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "invalid value in ZKAT-DLOG output metadata")
	}
	if err := dlogmath.CheckBaseElement(md.BlindingFactor, s.Legacy.PP.Curve); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "invalid blinding factor in ZKAT-DLOG output metadata")
	}
	clear, err := out.ToClear(md, s.Legacy.PP)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to open ZKAT-DLOG output")
	}
	recipients, err := s.Deserializer.Recipients(out.Owner)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to get recipients")
	}
	format, err := dlogtoken.SupportedTokenFormat(s.Legacy.PP, s.Legacy.PP.BitLength())
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed computing ZKAT-DLOG token format")
	}

	return clear, md.Issuer, recipients, format, nil
}

func (s *TokensService) Recipients(output driver.TokenOutput) ([]driver.Identity, error) {
	if IsDLogOutput(output) {
		if s.Legacy == nil {
			return nil, errors.New("ZKAT-DLOG output, but no ZKAT-DLOG public parameters are configured")
		}
		out := &dlogtoken.Token{}
		if err := out.Deserialize(output); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal ZKAT-DLOG output")
		}

		return s.Deserializer.Recipients(out.Owner)
	}
	if IsFabtokenOutput(output) {
		out := &actions.Output{}
		if err := out.Deserialize(output); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal fabtoken output")
		}

		return s.Deserializer.Recipients(out.Owner)
	}
	outputDesc, err := DeserializeOutputDescription(output)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal output")
	}
	recipients, err := s.Deserializer.Recipients(outputDesc.Recipient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipients")
	}

	return recipients, nil
}

// TokensUpgradeService is the burn-and-re-issue upgrade service of the
// zkatsnark driver. It reuses the zkatdlog protocol (challenge, per-owner
// signature over the challenge and the tokens, issuer-side verification), but
// only for legacy fabtoken outputs that zkatsnark cannot spend directly: the
// eligible formats are exactly the fabtoken formats absent from
// supportedTokenFormats (see InPlaceFabtokenPrecisions), so a token is either
// upgraded in place inside a transfer or by burn-and-re-issue, never both.
// It also accepts the ZKAT-DLOG outputs of the single ZKAT-DLOG generation
// embedded in the public parameters, resolved through a LegacyDLogResolver;
// commitment tokens of any other generation are refused.
type TokensUpgradeService = upgrade.Service

// NewTokensUpgradeService returns the zkatsnark TokensUpgradeService. precision
// is the zkatsnark quantity precision, used to parse fabtoken quantities;
// supportedTokenFormats are the formats zkatsnark spends directly, which are
// never upgrade-eligible. legacy resolves the ZKAT-DLOG formats that can be
// upgraded; nil restricts the service to fabtoken outputs.
func NewTokensUpgradeService(
	logger logging.Logger,
	precision uint64,
	deserializer upgrade.Deserializer,
	identityProvider upgrade.IdentityProvider,
	supportedTokenFormats []token.Format,
	legacy upgrade.PublicParamsResolver,
) (*TokensUpgradeService, error) {
	// the complement of the fabtoken formats TokensService spends directly
	var fabtokenFormats []token.Format
	for _, p := range fabtokenPrecisions {
		format, err := fabtoken.SupportedTokenFormat(p)
		if err != nil {
			return nil, errors.Wrapf(err, "failed computing fabtoken token format with precision [%d]", p)
		}
		if !slices.Contains(supportedTokenFormats, format) {
			fabtokenFormats = append(fabtokenFormats, format)
		}
	}

	return &upgrade.Service{
		Logger:                          logger,
		MaxPrecision:                    precision,
		UpgradeSupportedTokenFormatList: fabtokenFormats,
		SupportedTokenFormatList:        supportedTokenFormats,
		Deserializer:                    deserializer,
		IdentityProvider:                identityProvider,
		PublicParamsHistory:             legacy,
	}, nil
}

// fabtokenPrecisions are the precisions of the fabtoken formats that can be
// upgraded to zkatsnark.
var fabtokenPrecisions = []uint64{16, 32, 64}
