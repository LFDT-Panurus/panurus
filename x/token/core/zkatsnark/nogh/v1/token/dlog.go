/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math/big"
	"slices"

	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	dlogmath "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	dlogsetup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/comm"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/legacy"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// PedersenXYLen is the byte length of an uncompressed BLS12-381 G1 point,
// X || Y, as carried in a ZKAT-DLOG commitment.
const PedersenXYLen = 2 * legacy.PedersenCoordinateSize

// MigrationDescription proves that a redeemed ZKAT-DLOG commitment opens to
// the issue action's token type and to the value committed in
// (ValueCommitInX, ValueCommitInY). It never carries the commitment: the
// validator reads it from the ledger bytes of the redeemed token.
type MigrationDescription struct {
	// ValueCommitInX is the X coordinate of the value commitment of the
	// redeemed token, 32 bytes.
	ValueCommitInX []byte
	// ValueCommitInY is the Y coordinate of that value commitment, 32 bytes.
	ValueCommitInY []byte
	// MigrationProof is the proof for circuit.MigrationCircuit.
	MigrationProof []byte
}

// DLogInput is the public content of a ZKAT-DLOG output redeemed by an upgrade
// issuance.
type DLogInput struct {
	// Owner is the plain identity owning the token.
	Owner driver.Identity
	// PedersenXY is the uncompressed Pedersen commitment X || Y, as read from
	// the ledger bytes, PedersenXYLen bytes.
	PedersenXY []byte
}

// DLogOpening is the opening of a ZKAT-DLOG output.
type DLogOpening struct {
	DLogInput
	// Type is the committed token type.
	Type token2.Type
	// Value is the committed value.
	Value uint64
	// BlindingFactor is the Pedersen blinding factor, a canonical 32-byte
	// BLS12-381 scalar.
	BlindingFactor []byte
}

// IsDLogOutput reports whether raw is tagged as a ZKAT-DLOG (commitment)
// output. It does not check that the content is well-formed (see
// ParseDLogOutput).
func IsDLogOutput(raw []byte) bool {
	typed, err := tokens.UnmarshalTypedToken(raw)

	return err == nil && typed.Type == comm.Type
}

// LegacyDLogFormats returns the token formats of the outputs produced by the
// passed ZKAT-DLOG public parameters, as ZKAT-DLOG computes them.
func LegacyDLogFormats(legacyPP *dlogsetup.PublicParams) ([]token2.Format, error) {
	if legacyPP == nil {
		return nil, errors.New("nil ZKAT-DLOG public parameters")
	}

	return dlogtoken.CommTokenFormats(legacyPP)
}

// ParseDLogOutput parses raw, untrusted bytes as a ZKAT-DLOG output with a
// plain owner (x509, idemix or idemix nym) and a commitment that is a
// non-identity point of the curve of legacyPP, which must be a BLS12-381
// curve.
func ParseDLogOutput(raw []byte, legacyPP *dlogsetup.PublicParams) (*DLogInput, error) {
	if legacyPP == nil {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "ZKAT-DLOG public parameters not configured")
	}
	if !pp.IsLegacyDLogCurve(legacyPP.Curve) {
		return nil, errors.Wrapf(ErrInvalidUpgradeInput, "unsupported ZKAT-DLOG curve [%s]", legacyPP.Curve)
	}
	if !IsDLogOutput(raw) {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "not a ZKAT-DLOG output")
	}
	out := &dlogtoken.Token{}
	if err := out.Deserialize(raw); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "not a ZKAT-DLOG output"))
	}
	if err := checkPlainOwner(out.Owner); err != nil {
		return nil, err
	}
	if err := dlogmath.CheckElement(out.Data, legacyPP.Curve); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "invalid commitment"))
	}
	xy := out.Data.Bytes()
	if len(xy) != PedersenXYLen {
		return nil, errors.Wrapf(ErrInvalidUpgradeInput, "commitment has [%d] bytes, expected [%d]", len(xy), PedersenXYLen)
	}
	if err := legacy.CheckPedersenPoint(xy[:legacy.PedersenCoordinateSize], xy[legacy.PedersenCoordinateSize:]); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, err)
	}

	return &DLogInput{Owner: out.Owner, PedersenXY: xy}, nil
}

// OpenDLogOutput parses raw as ParseDLogOutput does and opens its commitment
// with metadataRaw, the ZKAT-DLOG token metadata, using the Pedersen
// generators of legacyPP. Both inputs are untrusted: the opening scalars are
// checked before any commitment arithmetic, and the value must fit in a
// uint64.
func OpenDLogOutput(raw, metadataRaw []byte, legacyPP *dlogsetup.PublicParams) (*DLogOpening, error) {
	in, err := ParseDLogOutput(raw, legacyPP)
	if err != nil {
		return nil, err
	}
	metadata := &dlogtoken.Metadata{}
	if err := metadata.Deserialize(metadataRaw); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "invalid ZKAT-DLOG token metadata"))
	}
	if len(metadata.Type) == 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "empty token type in opening")
	}
	if err := dlogmath.CheckBaseElement(metadata.Value, legacyPP.Curve); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "invalid value in opening"))
	}
	if err := dlogmath.CheckBaseElement(metadata.BlindingFactor, legacyPP.Curve); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "invalid blinding factor in opening"))
	}
	commitment := &dlogtoken.Token{}
	if err := commitment.Deserialize(raw); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, err)
	}
	if _, err := commitment.ToClear(metadata, legacyPP); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "opening does not match the commitment"))
	}
	valueInt := metadata.Value.BigInt()
	if !valueInt.IsUint64() {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "value does not fit in 64 bits")
	}
	bf := metadata.BlindingFactor.BigInt()
	if bf.Cmp(bls12381fr.Modulus()) >= 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "blinding factor not canonical")
	}

	return &DLogOpening{
		DLogInput:      *in,
		Type:           metadata.Type,
		Value:          valueInt.Uint64(),
		BlindingFactor: bf.FillBytes(make([]byte, bls12381fr.Bytes)),
	}, nil
}

// TokenTypePed returns the ZKAT-DLOG encoding of tokenType, the scalar the
// Pedersen commitment binds: SHA-256(tokenType) mod r, with r the BLS12-381
// scalar field order, exactly as mathlib's HashToZr computes it on every
// BLS12-381 curve.
func TokenTypePed(tokenType string) *big.Int {
	digest := sha256.Sum256([]byte(tokenType))

	return new(big.Int).Mod(new(big.Int).SetBytes(digest[:]), bls12381fr.Modulus())
}

// SummarizeDLogInputs parses every input as a ZKAT-DLOG output (see
// ParseDLogOutput) and checks that the list is non-empty and free of
// duplicate ledger IDs. Type and value are hidden by the commitments: the
// migration proofs bind them.
func SummarizeDLogInputs(inputs []*IssueActionInput, legacyPP *dlogsetup.PublicParams) ([]*DLogInput, error) {
	if len(inputs) == 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "no inputs")
	}
	res := make([]*DLogInput, len(inputs))
	seenIDs := make(map[token2.ID]struct{}, len(inputs))
	for i, in := range inputs {
		if in == nil || len(in.Token) == 0 || len(in.ID.TxId) == 0 {
			return nil, errors.Wrapf(ErrInvalidUpgradeInput, "input [%d] is nil or incomplete", i)
		}
		if _, ok := seenIDs[in.ID]; ok {
			return nil, errors.Wrapf(ErrDuplicateUpgradeInput, "input [%d] [%s]", i, in.ID)
		}
		seenIDs[in.ID] = struct{}{}
		parsed, err := ParseDLogOutput(in.Token, legacyPP)
		if err != nil {
			return nil, errors.WithMessagef(err, "input [%d]", i)
		}
		res[i] = parsed
	}

	return res, nil
}

// checkPlainOwner checks that owner is a non-empty x509, idemix or idemix nym
// identity, the only owners that can sign an upgrade proof.
func checkPlainOwner(owner driver.Identity) error {
	if len(owner) == 0 {
		return errors.Wrap(ErrInvalidUpgradeInput, "empty owner")
	}
	typed, err := identity.UnmarshalTypedIdentity(owner)
	if err != nil {
		return errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "malformed owner"))
	}
	switch typed.Type {
	case driver.X509IdentityType, driver.IdemixIdentityType, driver.IdemixNymIdentityType:
		return nil
	default:
		return errors.Wrapf(ErrInvalidUpgradeInput, "unsupported owner identity type [%d]", typed.Type)
	}
}

// LegacyDLogResolver implements upgrade.PublicParamsResolver over the single
// generation of ZKAT-DLOG public parameters embedded in the zkatsnark public
// parameters, so that the ZKAT-DLOG upgrade service accepts, and opens,
// exactly the outputs of that generation.
type LegacyDLogResolver struct {
	legacy  *pp.LegacyDLog
	formats []token2.Format
}

// NewLegacyDLogResolver returns a resolver for the passed ZKAT-DLOG public
// parameters.
func NewLegacyDLogResolver(legacyDLog *pp.LegacyDLog) (*LegacyDLogResolver, error) {
	if legacyDLog == nil || legacyDLog.PP == nil {
		return nil, errors.New("nil ZKAT-DLOG public parameters")
	}
	formats, err := LegacyDLogFormats(legacyDLog.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed computing ZKAT-DLOG token formats")
	}

	return &LegacyDLogResolver{legacy: legacyDLog, formats: formats}, nil
}

// Formats returns the ZKAT-DLOG token formats this resolver accepts.
func (r *LegacyDLogResolver) Formats() []token2.Format {
	return slices.Clone(r.formats)
}

// LegacyDLog returns the ZKAT-DLOG public parameters this resolver serves.
func (r *LegacyDLogResolver) LegacyDLog() *pp.LegacyDLog {
	return r.legacy
}

// ByFormat returns the ZKAT-DLOG public parameters and their hash if they
// produce format.
func (r *LegacyDLogResolver) ByFormat(_ context.Context, format token2.Format) (*dlogsetup.PublicParams, driver.PPHash, error) {
	if !slices.Contains(r.formats, format) {
		return nil, nil, errors.Wrapf(upgrade.ErrPublicParamsNotFound, "format [%s] is not a ZKAT-DLOG upgradable format", format)
	}

	return r.legacy.PP, slices.Clone(r.legacy.Hash), nil
}

// ByHashAndFormat returns the ZKAT-DLOG public parameters if they are stored
// under hash and produce format.
func (r *LegacyDLogResolver) ByHashAndFormat(_ context.Context, hash driver.PPHash, format token2.Format) (*dlogsetup.PublicParams, error) {
	if !bytes.Equal(hash, r.legacy.Hash) {
		return nil, errors.Wrapf(upgrade.ErrPublicParamsNotFound, "unknown ZKAT-DLOG public parameters hash")
	}
	if !slices.Contains(r.formats, format) {
		return nil, errors.Errorf("ZKAT-DLOG public parameters do not produce format [%s]", format)
	}

	return r.legacy.PP, nil
}

var _ upgrade.PublicParamsResolver = (*LegacyDLogResolver)(nil)
