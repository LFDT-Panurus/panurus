/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	errors2 "errors"
	"slices"

	math2 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/comm"
	utils2 "github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
)

// Precisions maps token formats to their corresponding bit-lengths.
var Precisions = map[token.Format]uint64{
	utils.MustGet(v1.SupportedTokenFormat(16)): 16,
	utils.MustGet(v1.SupportedTokenFormat(32)): 32,
	utils.MustGet(v1.SupportedTokenFormat(64)): 64,
}

// TokensService provides functions for managing ZKAT-DLOG tokens,
// including deobfuscation, serialization, and upgrading from Fabtoken.
type TokensService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*setup.PublicParams]
	IdentityDeserializer    driver.Deserializer

	// OutputTokenFormat is the default format used for output tokens.
	OutputTokenFormat token.Format
	// SupportedTokenFormatList lists all token formats this service can handle.
	SupportedTokenFormatList []token.Format
}

// NewTokensService creates a new TokensService and initializes its supported token formats.
func NewTokensService(logger logging.Logger, publicParametersManager common.PublicParametersManager[*setup.PublicParams], identityDeserializer driver.Deserializer) (*TokensService, error) {
	// compute supported tokens
	pp := publicParametersManager.PublicParams()
	maxPrecision := pp.BitLength()

	// dlog without graph hiding
	outputTokenFormat, err := SupportedTokenFormat(pp, maxPrecision)
	if err != nil {
		return nil, errors.Wrapf(err, "failed computing comm token types")
	}

	supportedTokenFormatList := make([]token.Format, 0, 3*len(pp.IdemixIssuerPublicKeys))
	for _, precision := range setup.SupportedPrecisions {
		// these Precisions are supported directly
		if precision <= maxPrecision {
			format, err := SupportedTokenFormat(pp, precision)
			if err != nil {
				return nil, errors.Wrapf(err, "failed computing comm token types")
			}
			supportedTokenFormatList = append(supportedTokenFormatList, format)
		}
	}

	// in addition, we support all fabtoken with precision less than maxPrecision
	for _, precision := range []uint64{16, 32, 64} {
		format, err := v1.SupportedTokenFormat(precision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed computing fabtoken token format with precision [%d]", precision)
		}
		if precision <= maxPrecision {
			supportedTokenFormatList = append(supportedTokenFormatList, format)
		}
	}

	return &TokensService{
		Logger:                   logger,
		PublicParametersManager:  publicParametersManager,
		IdentityDeserializer:     identityDeserializer,
		OutputTokenFormat:        outputTokenFormat,
		SupportedTokenFormatList: supportedTokenFormatList,
	}, nil
}

// Recipients returns the identities of the token recipients.
func (s *TokensService) Recipients(output driver.TokenOutput) ([]driver.Identity, error) {
	tok := &Token{}
	if err := tok.Deserialize(output); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize token")
	}
	recipients, err := s.IdentityDeserializer.Recipients(tok.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get recipients")
	}

	return recipients, nil
}

// Deobfuscate reveals the cleartext token and its issuer from an obfuscated output and its metadata.
// It first attempts to deobfuscate as a ZKAT-DLOG (commitment) token, falling back to Fabtoken if that fails.
func (s *TokensService) Deobfuscate(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	// we support fabtoken.Type and comm.Type

	// try first comm type
	tok, issuer, recipients, format, err := s.deobfuscateAsCommType(ctx, output, outputMetadata)
	if err == nil {
		return tok, issuer, recipients, format, nil
	}
	// try fabtoken type
	tok, issuer, recipients, format, err = s.deobfuscateAsFabtokenType(output, outputMetadata)
	if err != nil {
		return nil, nil, nil, "", errors.Wrapf(err, "failed to deobfuscate token")
	}

	return tok, issuer, recipients, format, nil
}

// deobfuscateAsCommType attempts to deobfuscate the token assuming it uses Pedersen commitments.
func (s *TokensService) deobfuscateAsCommType(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	_, metadata, tok, err := s.deserializeCommToken(ctx, output, outputMetadata, false)
	if err != nil {
		return nil, nil, nil, "", errors.Wrapf(err, "failed to deobfuscate token")
	}
	recipients, err := s.IdentityDeserializer.Recipients(tok.Owner)
	if err != nil {
		return nil, nil, nil, "", errors.Wrapf(err, "failed to get recipients")
	}

	return tok, metadata.Issuer, recipients, s.OutputTokenFormat, nil
}

// deobfuscateAsFabtokenType attempts to deobfuscate the token assuming it is a plain Fabtoken.
func (s *TokensService) deobfuscateAsFabtokenType(output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	// TODO: refer only to the protos
	tok := &actions.Output{}
	if err := tok.Deserialize(output); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed unmarshalling token")
	}

	metadata := &actions.OutputMetadata{}
	if err := metadata.Deserialize(outputMetadata); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed unmarshalling token information")
	}

	recipients, err := s.IdentityDeserializer.Recipients(tok.Owner)
	if err != nil {
		return nil, nil, nil, "", errors.Wrapf(err, "failed to get recipients")
	}

	return &token.Token{
		Owner:    tok.Owner,
		Type:     tok.Type,
		Quantity: tok.Quantity,
	}, metadata.Issuer, recipients, s.OutputTokenFormat, nil
}

// SupportedTokenFormats returns the list of all token formats supported by this service.
func (s *TokensService) SupportedTokenFormats() []token.Format {
	return s.SupportedTokenFormatList
}

// DeserializeToken unmarshals raw token data and metadata into their respective structures.
// It handles both ZKAT-DLOG tokens and automatic upgrades from Fabtoken to ZKAT-DLOG.
func (s *TokensService) DeserializeToken(ctx context.Context, outputFormat token.Format, outputRaw []byte, metadataRaw []byte) (*Token, *Metadata, *UpgradeWitness, error) {
	// Here we have to check if what we get in input is already as expected.
	// If not, we need to check if a token upgrade is possible.
	// If not, a failure is to be returned
	if !slices.Contains(s.SupportedTokenFormatList, outputFormat) {
		return nil, nil, nil, errors.Errorf("invalid token format [%s], expected one of [%v]", outputFormat, s.SupportedTokenFormatList)
	}

	if outputFormat == s.OutputTokenFormat {
		// deserialize token with output token format
		tok, meta, err := s.deserializeTokenWithOutputTokenFormat(ctx, outputRaw, metadataRaw)
		if err != nil {
			return nil, nil, nil, errors.Wrap(err, "failed to deserialize token with output token format")
		}

		return tok, meta, nil, nil
	}

	// if we reach this point, we need to upgrade the token locally
	_, ok := Precisions[outputFormat]
	if !ok {
		return nil, nil, nil, errors.Errorf("unsupported token format [%s]", outputFormat)
	}
	fabToken, value, err := ParseFabtokenToken(outputRaw, s.PublicParametersManager.PublicParams().QuantityPrecision)
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "failed to unmarshal fabtoken token")
	}
	pp := s.PublicParametersManager.PublicParams()
	curve := math2.Curves[pp.Curve]
	tokens, meta, err := GetTokensWithWitness([]uint64{value}, fabToken.Type, pp.PedersenGenerators, curve)
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "failed to compute commitment")
	}

	return &Token{
			Owner: fabToken.Owner,
			Data:  tokens[0],
		}, &Metadata{
			Type:           fabToken.Type,
			Value:          math.NewCachedZrFromInt(curve, value),
			BlindingFactor: meta[0].BlindingFactor,
		}, &UpgradeWitness{
			FabToken:       fabToken,
			BlindingFactor: meta[0].BlindingFactor,
		}, nil
}

// deserializeTokenWithOutputTokenFormat deserializes the token using the default ZKAT-DLOG format.
func (s *TokensService) deserializeTokenWithOutputTokenFormat(ctx context.Context, outputRaw []byte, metadataRaw []byte) (*Token, *Metadata, error) {
	// get zkatdlog token
	output, err := s.getOutput(ctx, outputRaw, false)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed getting token output")
	}

	// get metadata
	metadata := &Metadata{}
	err = metadata.Deserialize(metadataRaw)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to deserialize token metadata")
	}

	return output, metadata, nil
}

// deserializeCommToken deserializes and verifies a commitment-based token.
func (s *TokensService) deserializeCommToken(ctx context.Context, outputRaw []byte, metadataRaw []byte, checkOwner bool) (*Token, *Metadata, *token.Token, error) {
	// get zkatdlog token
	output, err := s.getOutput(ctx, outputRaw, checkOwner)
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "failed getting token output")
	}

	// get metadata
	metadata := &Metadata{}
	err = metadata.Deserialize(metadataRaw)
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "failed to deserialize token metadata [%d][%v]", len(metadataRaw), metadataRaw)
	}
	pp := s.PublicParametersManager.PublicParams()

	tok, err := output.ToClear(metadata, pp)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to deserialize token")
	}

	return output, metadata, tok, nil
}

// getOutput unmarshals and validates the token output from raw bytes.
func (s *TokensService) getOutput(ctx context.Context, outputRaw []byte, checkOwner bool) (*Token, error) {
	output := &Token{}
	if err := output.Deserialize(outputRaw); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize token")
	}
	if checkOwner && len(output.Owner) == 0 {
		return nil, errors.Errorf("token owner not found in output")
	}
	if err := math.CheckElement(output.Data, s.PublicParametersManager.PublicParams().Curve); err != nil {
		return nil, errors.Wrap(err, "data is invalid in output")
	}

	return output, nil
}

// SupportedTokenFormat computes a unique token format identifier based on public parameters and precision.
func SupportedTokenFormat(pp *setup.PublicParams, precision uint64) (token.Format, error) {
	hasher := utils2.NewSHA256Hasher()
	if err := errors2.Join(
		hasher.AddInt32(comm.Type),
		hasher.AddInt(int(pp.Curve)),
		hasher.AddUInt64(precision),
		hasher.AddG1s(pp.PedersenGenerators),
	); err != nil {
		return "", errors.Wrapf(err, "failed to generate token format")
	}

	return token.Format(hasher.HexDigest()), nil
}
