/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	math "github.com/IBM/mathlib"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	noghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/comm"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

// Token represents a ZKAT-DLOG token without graph hiding.
// It encodes the token owner and the Pedersen commitment to its type, value, and blinding factor.
type Token comm.Token

// GetOwner returns the owner of the token.
func (t *Token) GetOwner() []byte {
	return t.Owner
}

// IsRedeem returns true if the token is a redemption (i.e., has no owner).
func (t *Token) IsRedeem() bool {
	return len(t.Owner) == 0
}

// Serialize marshals the Token into bytes, including its type information for proper unwrapping.
func (t *Token) Serialize() ([]byte, error) {
	data, err := utils.ToProtoG1(t.Data)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize output")
	}
	raw, err := proto.Marshal(&actions.Token{
		Owner: t.Owner,
		Data:  data,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed serializing token")
	}

	return comm.WrapTokenWithType(raw)
}

// Deserialize unmarshals the Token from bytes and validates its type.
func (t *Token) Deserialize(bytes []byte) error {
	typed, err := comm.UnmarshalTypedToken(bytes)
	if err != nil {
		return errors.Wrapf(err, "failed deserializing token")
	}
	if typed.Type != comm.Type {
		return errors.Errorf("invalid token type [%v]", typed.Type)
	}
	token := &actions.Token{}
	if err := proto.Unmarshal(typed.Token, token); err != nil {
		return errors.Wrapf(err, "failed unmarshalling token")
	}
	t.Owner = token.Owner
	t.Data, err = utils.FromG1Proto(token.Data)

	return err
}

// ToClear verifies the token commitment against the provided metadata and public parameters.
// If valid, it returns the token in cleartext (type, quantity, and owner).
func (t *Token) ToClear(meta *Metadata, pp *noghv1.PublicParams) (*token.Token, error) {
	com, err := commit([]*math.Zr{
		math.Curves[pp.Curve].HashToZr([]byte(meta.Type)),
		meta.Value,
		meta.BlindingFactor,
	}, pp.PedersenGenerators, math.Curves[pp.Curve])
	if err != nil {
		return nil, errors.Wrap(err, "cannot retrieve token in the clear: failed to check token data")
	}
	// check that token matches meta
	if !com.Equals(t.Data) {
		return nil, ErrTokenMismatch
	}

	return &token.Token{
		Type:     meta.Type,
		Quantity: "0x" + meta.Value.String(),
		Owner:    t.Owner,
	}, nil
}

// Validate checks if the token structure is well-formed.
func (t *Token) Validate(checkOwner bool) error {
	if checkOwner && len(t.Owner) == 0 {
		return ErrEmptyOwner
	}
	if t.Data == nil {
		return ErrEmptyTokenData
	}

	return nil
}

// computeTokens generates Pedersen commitments for a list of token metadata.
func computeTokens(tw []*Metadata, pp []*math.G1, c *math.Curve) ([]*math.G1, error) {
	tokens := make([]*math.G1, len(tw))
	var err error
	for i := range tw {
		hash := c.HashToZr([]byte(tw[i].Type))
		tokens[i], err = commit([]*math.Zr{hash, tw[i].Value, tw[i].BlindingFactor}, pp, c)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to compute token [%d]", i)
		}
	}

	return tokens, nil
}

// GetTokensWithWitness generates commitments and metadata for a given set of values and token type.
// It uses a cryptographically secure random number generator for blinding factors.
func GetTokensWithWitness(values []uint64, tokenType token.Type, pp []*math.G1, c *math.Curve) ([]*math.G1, []*Metadata, error) {
	if c == nil {
		return nil, nil, errors.New("cannot get tokens with witness: please initialize curve")
	}
	rand, err := c.Rand()
	if err != nil {
		return nil, nil, errors.Wrap(err, "cannot get tokens with witness")
	}
	bfs := make([]*math.Zr, len(values))
	for i := range values {
		bfs[i] = c.NewRandomZr(rand)
	}

	return GetTokensWithWitnessAndBF(values, bfs, tokenType, pp, c)
}

// GetTokensWithWitnessAndBF returns token commitments and metadata for the passed values, blinding factors, and type.
// This is useful for recomputing commitments during validation or testing.
func GetTokensWithWitnessAndBF(values []uint64, bfs []*math.Zr, tokenType token.Type, pp []*math.G1, c *math.Curve) ([]*math.G1, []*Metadata, error) {
	if c == nil {
		return nil, nil, errors.New("cannot get tokens with witness: please initialize curve")
	}
	if len(values) != len(bfs) {
		return nil, nil, errors.New("cannot get tokens with witness: values and bfs must have the same length")
	}
	tw := make([]*Metadata, len(values))
	for i, v := range values {
		tw[i] = &Metadata{
			BlindingFactor: bfs[i],
			Value:          math2.NewCachedZrFromInt(c, v),
			Type:           tokenType,
		}
	}
	tokens, err := computeTokens(tw, pp, c)
	if err != nil {
		return nil, nil, errors.Wrap(err, "cannot get tokens with witness")
	}

	return tokens, tw, nil
}

// Metadata contains the opening information (type, value, blinding factor) for a token commitment.
type Metadata comm.Metadata

// NewMetadata creates a slice of Metadata objects from the provided values and blinding factors.
func NewMetadata(curve math.CurveID, tokenType token.Type, values []uint64, bfs []*math.Zr) []*Metadata {
	witness := make([]*Metadata, len(values))
	for i, v := range values {
		witness[i] = &Metadata{
			Value:          math2.NewCachedZrFromInt(math.Curves[curve], v),
			BlindingFactor: bfs[i],
		}
		witness[i].Type = tokenType
	}

	return witness
}

// Deserialize unmarshals Metadata from bytes and validates its structure.
func (m *Metadata) Deserialize(b []byte) error {
	typed, err := comm.UnmarshalTypedToken(b)
	if err != nil {
		return errors.Wrapf(err, "failed deserializing metadata")
	}
	metadata := &actions.TokenMetadata{}
	if err := proto.Unmarshal(typed.Token, metadata); err != nil {
		return errors.Wrapf(err, "failed unmarshalling metadata")
	}
	m.Type = token.Type(metadata.Type)
	m.Value, err = utils.FromZrProto(metadata.Value)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize metadata")
	}
	m.BlindingFactor, err = utils.FromZrProto(metadata.BlindingFactor)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize metadata")
	}
	if metadata.Issuer != nil {
		m.Issuer = metadata.Issuer.Raw
	}

	return nil
}

// Serialize marshals Metadata into bytes, including its type information for proper unwrapping.
func (m *Metadata) Serialize() ([]byte, error) {
	value, err := utils.ToProtoZr(m.Value)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize metadata")
	}
	blindingFactor, err := utils.ToProtoZr(m.BlindingFactor)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize metadata")
	}
	raw, err := proto.Marshal(&actions.TokenMetadata{
		Type:           string(m.Type),
		Value:          value,
		BlindingFactor: blindingFactor,
		Issuer:         &protosv1.Identity{Raw: m.Issuer},
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed serializing token")
	}

	return comm.WrapMetadataWithType(raw)
}

// Clone creates a deep copy of the Metadata.
func (m *Metadata) Clone() *Metadata {
	return &Metadata{
		Type:           m.Type,
		BlindingFactor: m.BlindingFactor,
		Issuer:         m.Issuer,
		Value:          m.Value,
	}
}

// Validate ensures the Metadata is well-formed and checks the presence of the issuer if required.
func (m *Metadata) Validate(checkIssuer bool) error {
	if len(m.Type) == 0 {
		return ErrEmptyType
	}
	if m.Value == nil {
		return ErrEmptyValue
	}
	if m.BlindingFactor == nil {
		return ErrEmptyBlindingFactor
	}
	if checkIssuer && len(m.Issuer) == 0 {
		return ErrMissingIssuer
	}
	if !checkIssuer && len(m.Issuer) != 0 {
		return ErrUnexpectedIssuer
	}

	return nil
}

// commit computes a Pedersen commitment to a vector of field elements using the provided generators.
func commit(vector []*math.Zr, generators []*math.G1, c *math.Curve) (*math.G1, error) {
	com := c.NewG1()
	for i := range vector {
		if vector[i] == nil {
			return nil, ErrNilCommitElement
		}
		com.Add(generators[i].Mul(vector[i]))
	}

	return com, nil
}

// UpgradeWitness contains the original Fabtoken output and the blinding factor
// used to create the upgraded ZKAT-DLOG commitment.
type UpgradeWitness struct {
	FabToken *fabtokenv1.Output
	// BlindingFactor is the blinding factor used to commit type and value
	BlindingFactor *math.Zr
}

// Validate ensures the UpgradeWitness is well-formed.
func (u *UpgradeWitness) Validate() error {
	if u.FabToken == nil {
		return ErrMissingFabToken
	}
	if len(u.FabToken.Owner) == 0 {
		return ErrMissingFabTokenOwner
	}
	if len(u.FabToken.Type) == 0 {
		return ErrMissingFabTokenType
	}
	if len(u.FabToken.Quantity) == 0 {
		return ErrMissingFabTokenQuantity
	}
	if u.BlindingFactor == nil {
		return ErrMissingUpgradeBlindingFactor
	}

	return nil
}
