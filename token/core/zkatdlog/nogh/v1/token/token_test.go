/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	actions2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	noghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/comm"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToken(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)

	owner := []byte("owner1")
	data := c.GenG1.Mul(c.NewRandomZr(rand))

	tok := &Token{
		Owner: owner,
		Data:  data,
	}

	assert.Equal(t, owner, tok.GetOwner())
	assert.False(t, tok.IsRedeem())

	tok.Owner = nil
	assert.True(t, tok.IsRedeem())

	tok.Owner = owner
	// Test Serialize/Deserialize
	raw, err := tok.Serialize()
	require.NoError(t, err)

	tok2 := &Token{}
	err = tok2.Deserialize(raw)
	require.NoError(t, err)
	assert.Equal(t, tok.Owner, tok2.Owner)
	assert.True(t, tok.Data.Equals(tok2.Data))

	// Test Validate
	require.NoError(t, tok.Validate(true))
	require.NoError(t, tok.Validate(false))

	tok.Owner = nil
	require.Error(t, tok.Validate(true))
	assert.Equal(t, ErrEmptyOwner, tok.Validate(true))

	tok.Data = nil
	require.Error(t, tok.Validate(false))
	assert.Equal(t, ErrEmptyTokenData, tok.Validate(false))
}

func TestToken_ToClear(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
	}

	meta := &Metadata{
		Type:           "COIN",
		Value:          c.NewZrFromUint64(100),
		BlindingFactor: c.NewRandomZr(rand),
	}

	// Valid case
	data, err := commit([]*math.Zr{
		c.HashToZr([]byte(meta.Type)),
		meta.Value,
		meta.BlindingFactor,
	}, pp.PedersenGenerators, c)
	require.NoError(t, err)

	tok := &Token{
		Owner: []byte("owner"),
		Data:  data,
	}

	clearToken, err := tok.ToClear(meta, pp)
	require.NoError(t, err)
	assert.Equal(t, meta.Type, clearToken.Type)
	assert.Equal(t, "0x"+meta.Value.String(), clearToken.Quantity)
	assert.Equal(t, tok.Owner, clearToken.Owner)

	// Mismatch case
	meta2 := meta.Clone()
	meta2.Value = c.NewZrFromUint64(200)
	_, err = tok.ToClear(meta2, pp)
	require.Error(t, err)
	assert.Equal(t, ErrTokenMismatch, err)
}

func TestMetadata(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)

	values := []uint64{10, 20}
	bfs := []*math.Zr{c.NewRandomZr(rand), c.NewRandomZr(rand)}
	metas := NewMetadata(curve, "COIN", values, bfs)
	assert.Len(t, metas, 2)
	assert.Equal(t, "COIN", string(metas[0].Type))
	assert.True(t, c.NewZrFromUint64(10).Equals(metas[0].Value))

	// Serialize/Deserialize
	meta := metas[0]
	meta.Issuer = []byte("issuer")
	raw, err := meta.Serialize()
	require.NoError(t, err)

	meta2 := &Metadata{}
	err = meta2.Deserialize(raw)
	require.NoError(t, err)
	assert.Equal(t, meta.Type, meta2.Type)
	assert.True(t, meta.Value.Equals(meta2.Value))
	assert.True(t, meta.BlindingFactor.Equals(meta2.BlindingFactor))
	assert.Equal(t, meta.Issuer, meta2.Issuer)

	// Clone
	meta3 := meta.Clone()
	assert.Equal(t, meta.Type, meta3.Type)
	assert.True(t, meta.Value.Equals(meta3.Value))
	assert.True(t, meta.BlindingFactor.Equals(meta3.BlindingFactor))
	assert.Equal(t, meta.Issuer, meta3.Issuer)
}

func TestMetadata_Validate(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)

	validMetadata := &Metadata{
		Type:           "COIN",
		Value:          c.NewRandomZr(rand),
		BlindingFactor: c.NewRandomZr(rand),
		Issuer:         []byte("issuer1"),
	}

	tests := []struct {
		name        string
		meta        *Metadata
		checkIssuer bool
		wantErr     string
	}{
		{
			name:        "valid metadata",
			meta:        validMetadata,
			checkIssuer: true,
			wantErr:     "",
		},
		{
			name: "missing type",
			meta: &Metadata{
				Type:           "",
				Value:          validMetadata.Value,
				BlindingFactor: validMetadata.BlindingFactor,
				Issuer:         validMetadata.Issuer,
			},
			checkIssuer: true,
			wantErr:     ErrEmptyType.Error(),
		},
		{
			name: "missing value",
			meta: &Metadata{
				Type:           validMetadata.Type,
				Value:          nil,
				BlindingFactor: validMetadata.BlindingFactor,
				Issuer:         validMetadata.Issuer,
			},
			checkIssuer: true,
			wantErr:     ErrEmptyValue.Error(),
		},
		{
			name: "missing blinding factor",
			meta: &Metadata{
				Type:           validMetadata.Type,
				Value:          validMetadata.Value,
				BlindingFactor: nil,
				Issuer:         validMetadata.Issuer,
			},
			checkIssuer: true,
			wantErr:     ErrEmptyBlindingFactor.Error(),
		},
		{
			name: "missing issuer",
			meta: &Metadata{
				Type:           validMetadata.Type,
				Value:          validMetadata.Value,
				BlindingFactor: validMetadata.BlindingFactor,
				Issuer:         nil,
			},
			checkIssuer: true,
			wantErr:     ErrMissingIssuer.Error(),
		},
		{
			name: "should not have the issuer",
			meta: &Metadata{
				Type:           validMetadata.Type,
				Value:          validMetadata.Value,
				BlindingFactor: validMetadata.BlindingFactor,
				Issuer:         validMetadata.Issuer,
			},
			checkIssuer: false,
			wantErr:     ErrUnexpectedIssuer.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.meta.Validate(tt.checkIssuer)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestGetTokensWithWitness(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := []*math.G1{
		c.GenG1.Mul(c.NewRandomZr(rand)),
		c.GenG1.Mul(c.NewRandomZr(rand)),
		c.GenG1.Mul(c.NewRandomZr(rand)),
	}

	tokens, metas, err := GetTokensWithWitness([]uint64{10, 20}, "COIN", pp, c)
	require.NoError(t, err)
	assert.Len(t, tokens, 2)
	assert.Len(t, metas, 2)

	// Verify tokens match metas
	for i := range tokens {
		expected, err := commit([]*math.Zr{
			c.HashToZr([]byte(metas[i].Type)),
			metas[i].Value,
			metas[i].BlindingFactor,
		}, pp, c)
		require.NoError(t, err)
		assert.True(t, expected.Equals(tokens[i]))
	}

	// Error cases
	_, _, err = GetTokensWithWitness([]uint64{10}, "COIN", pp, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "please initialize curve")
}

func TestUpgradeWitness_Validate(t *testing.T) {
	validFabToken := &actions.Output{
		Owner:    []byte("owner"),
		Type:     "COIN",
		Quantity: "0x10",
	}
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	bf := c.NewRandomZr(rand)

	tests := []struct {
		name    string
		uw      *UpgradeWitness
		wantErr string
	}{
		{
			name: "valid",
			uw: &UpgradeWitness{
				FabToken:       validFabToken,
				BlindingFactor: bf,
			},
			wantErr: "",
		},
		{
			name: "missing fabtoken",
			uw: &UpgradeWitness{
				FabToken:       nil,
				BlindingFactor: bf,
			},
			wantErr: ErrMissingFabToken.Error(),
		},
		{
			name: "missing owner",
			uw: &UpgradeWitness{
				FabToken: &actions.Output{
					Owner:    nil,
					Type:     "COIN",
					Quantity: "0x10",
				},
				BlindingFactor: bf,
			},
			wantErr: ErrMissingFabTokenOwner.Error(),
		},
		{
			name: "missing type",
			uw: &UpgradeWitness{
				FabToken: &actions.Output{
					Owner:    []byte("owner"),
					Type:     "",
					Quantity: "0x10",
				},
				BlindingFactor: bf,
			},
			wantErr: ErrMissingFabTokenType.Error(),
		},
		{
			name: "missing quantity",
			uw: &UpgradeWitness{
				FabToken: &actions.Output{
					Owner:    []byte("owner"),
					Type:     "COIN",
					Quantity: "",
				},
				BlindingFactor: bf,
			},
			wantErr: ErrMissingFabTokenQuantity.Error(),
		},
		{
			name: "missing blinding factor",
			uw: &UpgradeWitness{
				FabToken:       validFabToken,
				BlindingFactor: nil,
			},
			wantErr: ErrMissingUpgradeBlindingFactor.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.uw.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestInternal(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	gens := []*math.G1{
		c.GenG1.Mul(c.NewRandomZr(rand)),
		c.GenG1.Mul(c.NewRandomZr(rand)),
	}

	// Test commit error
	_, err = commit([]*math.Zr{nil}, gens, c)
	require.Error(t, err)
	assert.Equal(t, ErrNilCommitElement, err)

	// Test computeTokens error
	_, err = computeTokens([]*Metadata{{Type: "COIN", Value: nil}}, gens, c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compute token")
}

func TestToken_DeserializeExtra(t *testing.T) {
	tok := &Token{}

	// failed to unmarshal typed token
	err := tok.Deserialize([]byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed deserializing token")

	// invalid token type
	raw, _ := tokens.WrapWithType(3, []byte("data"))
	err = tok.Deserialize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token type [3]")

	// failed to unmarshal token
	typed := &tokens.TypedToken{
		Type:  comm.Type,
		Token: []byte("invalid proto"),
	}
	raw, _ = typed.Bytes()
	err = tok.Deserialize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling token")
}

func TestMetadata_DeserializeExtra(t *testing.T) {
	m := &Metadata{}

	// failed to unmarshal typed token
	err := m.Deserialize([]byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed deserializing metadata")

	// failed to unmarshal metadata
	typed := &tokens.TypedMetadata{
		Type:     comm.Type,
		Metadata: []byte("invalid proto"),
	}
	raw, _ := typed.Bytes()
	err = m.Deserialize(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling metadata")

	// failed to deserialize value
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	valueRaw, _ := utils.ToProtoZr(c.NewRandomZr(rand))
	bfRaw, _ := utils.ToProtoZr(c.NewRandomZr(rand))

	metaProto := &actions2.TokenMetadata{
		Type:           "COIN",
		Value:          valueRaw,
		BlindingFactor: bfRaw,
	}
	// Corrupt Value
	metaProto.Value.Raw = []byte("invalid")
	rawProto, _ := proto.Marshal(metaProto)
	rawTyped, _ := comm.WrapMetadataWithType(rawProto)
	err = m.Deserialize(rawTyped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize metadata")

	// Corrupt BlindingFactor
	metaProto.Value.Raw = valueRaw.Raw
	metaProto.BlindingFactor.Raw = []byte("invalid")
	rawProto, _ = proto.Marshal(metaProto)
	rawTyped, _ = comm.WrapMetadataWithType(rawProto)
	err = m.Deserialize(rawTyped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize metadata")
}

type mockPPM struct {
	pp *noghv1.PublicParams
}

func (m *mockPPM) PublicParams() *noghv1.PublicParams           { return m.pp }
func (m *mockPPM) PublicParameters() driver.PublicParameters    { return m.pp }
func (m *mockPPM) NewCertifierKeyPair() ([]byte, []byte, error) { return nil, nil, nil }
func (m *mockPPM) PublicParamsHash() driver.PPHash              { return nil }

func TestTokensService(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
		QuantityPrecision: 64,
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	logger := logging.MustGetLogger("test")

	s, err := NewTokensService(logger, ppm, deserializer)
	require.NoError(t, err)
	assert.NotNil(t, s)

	// Test Recipients
	owner := []byte("owner")
	meta := &Metadata{
		Type:           "COIN",
		Value:          c.NewZrFromUint64(100),
		BlindingFactor: c.NewRandomZr(rand),
		Issuer:         []byte("issuer"),
	}
	data, err := commit([]*math.Zr{
		c.HashToZr([]byte(meta.Type)),
		meta.Value,
		meta.BlindingFactor,
	}, pp.PedersenGenerators, c)
	require.NoError(t, err)

	tok := &Token{
		Owner: owner,
		Data:  data,
	}
	rawTok, _ := tok.Serialize()

	deserializer.RecipientsReturns([]driver.Identity{driver.Identity("id1")}, nil)
	recipients, err := s.Recipients(rawTok)
	require.NoError(t, err)
	assert.Equal(t, []driver.Identity{driver.Identity("id1")}, recipients)

	// Test Deobfuscate
	rawMeta, _ := meta.Serialize()

	deobTok, issuer, deobRecipients, format, err := s.Deobfuscate(context.Background(), rawTok, rawMeta)
	require.NoError(t, err)
	assert.Equal(t, "COIN", string(deobTok.Type))
	assert.Equal(t, "0x"+meta.Value.String(), deobTok.Quantity)
	assert.Equal(t, driver.Identity("issuer"), issuer)
	assert.Equal(t, []driver.Identity{driver.Identity("id1")}, deobRecipients)
	assert.Equal(t, s.OutputTokenFormat, format)

	// Test Deobfuscate as Fabtoken
	fabTokenOutput := &actions.Output{
		Owner:    owner,
		Type:     "COIN",
		Quantity: "0x10",
	}
	rawFabTok, _ := fabTokenOutput.Serialize()
	fabMeta := &actions.OutputMetadata{
		Issuer: []byte("issuer2"),
	}
	rawFabMeta, _ := fabMeta.Serialize()

	deserializer.RecipientsReturns([]driver.Identity{driver.Identity("id2")}, nil)
	deobTok2, issuer2, deobRecipients2, _, err := s.Deobfuscate(context.Background(), rawFabTok, rawFabMeta)
	require.NoError(t, err)
	assert.Equal(t, "COIN", string(deobTok2.Type))
	assert.Equal(t, "0x10", deobTok2.Quantity)
	assert.Equal(t, driver.Identity("issuer2"), issuer2)
	assert.Equal(t, []driver.Identity{driver.Identity("id2")}, deobRecipients2)

	// Error cases for Recipients
	deserializer.RecipientsReturns(nil, errors.New("error"))
	_, err = s.Recipients(rawTok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get recipients")

	// Error cases for Deobfuscate
	_, _, _, _, err = s.Deobfuscate(context.Background(), []byte("invalid"), rawMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deobfuscate token")

	// Test SupportedTokenFormats
	formats := s.SupportedTokenFormats()
	assert.Contains(t, formats, s.OutputTokenFormat)

	// Test DeserializeToken
	desTok, desMeta, desWitness, err := s.DeserializeToken(context.Background(), s.OutputTokenFormat, rawTok, rawMeta)
	require.NoError(t, err)
	assert.Equal(t, tok.Owner, desTok.Owner)
	assert.Equal(t, meta.Type, desMeta.Type)
	assert.Nil(t, desWitness)

	// Test DeserializeToken with error
	_, _, _, err = s.DeserializeToken(context.Background(), "invalid", rawTok, rawMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token format [invalid]")

	_, _, _, err = s.DeserializeToken(context.Background(), s.OutputTokenFormat, []byte("invalid"), rawMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize token with output token format")

	// Test DeserializeToken with Upgrade
	fabToken := &actions.Output{
		Owner:    []byte("owner"),
		Type:     "COIN",
		Quantity: "0x10",
	}
	rawFabToken, _ := fabToken.Serialize()
	fabFormat, _ := v1.SupportedTokenFormat(64)

	// We need to make sure fabFormat is in s.SupportedTokenFormatList
	s.SupportedTokenFormatList = append(s.SupportedTokenFormatList, fabFormat)
	Precisions[fabFormat] = 64

	desTok2, desMeta2, desWitness2, err := s.DeserializeToken(context.Background(), fabFormat, rawFabToken, nil)
	require.NoError(t, err)
	assert.Equal(t, fabToken.Owner, desTok2.Owner)
	assert.Equal(t, fabToken.Type, desMeta2.Type)
	assert.NotNil(t, desWitness2)

	// Upgrade with invalid token format
	invalidFabFormat := token.Format("invalid-fab")
	s.SupportedTokenFormatList = append(s.SupportedTokenFormatList, invalidFabFormat)
	_, _, _, err = s.DeserializeToken(context.Background(), invalidFabFormat, rawFabToken, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported token format [invalid-fab]")
}

func TestParseFabtokenToken(t *testing.T) {
	output := &actions.Output{
		Owner:    []byte("owner"),
		Type:     "COIN",
		Quantity: "0x10",
	}
	raw, _ := output.Serialize()

	parsed, q, err := ParseFabtokenToken(raw, 64)
	require.NoError(t, err)
	assert.Equal(t, output.Owner, parsed.Owner)
	assert.Equal(t, uint64(16), q)

	// Error cases
	_, _, err = ParseFabtokenToken(raw, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create quantity: 0x10 has precision 5 > 2")

	_, _, err = ParseFabtokenToken([]byte("invalid"), 64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal fabtoken")

	output.Quantity = "invalid"
	raw, _ = output.Serialize()
	_, _, err = ParseFabtokenToken(raw, 64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create quantity")
}

func TestToken_ToClear_Extra(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
	}

	tok := &Token{
		Owner: []byte("owner"),
		Data:  c.GenG1,
	}

	// Nil Value
	meta := &Metadata{
		Type:           "COIN",
		Value:          nil,
		BlindingFactor: c.NewRandomZr(rand),
	}
	_, err = tok.ToClear(meta, pp)
	require.Error(t, err)
	assert.True(t, errors.HasCause(err, ErrNilCommitElement))
	assert.Contains(t, err.Error(), "cannot retrieve token in the clear: failed to check token data")

	// Nil BlindingFactor
	meta = &Metadata{
		Type:           "COIN",
		Value:          c.NewRandomZr(rand),
		BlindingFactor: nil,
	}
	_, err = tok.ToClear(meta, pp)
	require.Error(t, err)
	assert.True(t, errors.HasCause(err, ErrNilCommitElement))
}

func TestTokensService_New_Extra(t *testing.T) {
	logger := logging.MustGetLogger("test")
	deserializer := &mock.Deserializer{}

	// Case 1: SupportedTokenFormat failure due to nil generators
	pp := &noghv1.PublicParams{
		Curve: math.BN254,
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
		PedersenGenerators: []*math.G1{nil, nil, nil},
	}
	ppm := &mockPPM{pp: pp}
	assert.Panics(t, func() {
		_, _ = NewTokensService(logger, ppm, deserializer)
	})
}

func TestTokensService_getOutput_Extra(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	logger := logging.MustGetLogger("test")
	s, err := NewTokensService(logger, ppm, deserializer)
	require.NoError(t, err)

	// failed to deserialize token
	_, err = s.getOutput(context.Background(), []byte("invalid"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize token")

	// token owner not found
	tok := &Token{Owner: nil, Data: c.GenG1}
	raw, _ := tok.Serialize()
	_, err = s.getOutput(context.Background(), raw, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token owner not found in output")

	// data is invalid (nil)
	tok = &Token{Owner: []byte("owner"), Data: nil}
	raw, _ = tok.Serialize()
	_, err = s.getOutput(context.Background(), raw, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data is invalid in output")
	assert.Contains(t, err.Error(), "element is nil")

	// data is invalid (wrong curve)
	tok = &Token{Owner: []byte("owner"), Data: math.Curves[math.BLS12_381_GURVY].GenG1}
	raw, _ = tok.Serialize()
	_, err = s.getOutput(context.Background(), raw, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data is invalid in output")
	assert.Contains(t, err.Error(), "element curve must equal curve ID")

	// data is infinity
	tok = &Token{Owner: []byte("owner"), Data: c.NewG1()}
	raw, _ = tok.Serialize()
	_, err = s.getOutput(context.Background(), raw, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data is invalid in output")
	assert.Contains(t, err.Error(), "element is infinity")
}

func TestTokensService_Deobfuscate_Extra(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	logger := logging.MustGetLogger("test")
	s, err := NewTokensService(logger, ppm, deserializer)
	require.NoError(t, err)

	tok := &Token{Owner: []byte("owner"), Data: c.GenG1}
	rawTok, _ := tok.Serialize()
	meta := &Metadata{Type: "COIN", Value: c.NewZrFromUint64(10), BlindingFactor: c.NewRandomZr(rand)}
	rawMeta, _ := meta.Serialize()

	// deobfuscateAsCommType fails at ToClear (mismatch)
	_, _, _, _, err = s.Deobfuscate(context.Background(), rawTok, rawMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deobfuscate token")

	// deobfuscateAsFabtokenType fails at Deserialize output
	_, _, _, _, err = s.deobfuscateAsFabtokenType([]byte("invalid"), rawMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling token")

	// deobfuscateAsFabtokenType fails at Deserialize metadata
	fabTok := &actions.Output{Owner: []byte("owner"), Type: "COIN", Quantity: "0x10"}
	rawFabTok, _ := fabTok.Serialize()
	_, _, _, _, err = s.deobfuscateAsFabtokenType(rawFabTok, []byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling token information")

	// deobfuscateAsFabtokenType fails at Recipients
	deserializer.RecipientsReturns(nil, errors.New("recipients error"))
	rawFabMeta, _ := (&actions.OutputMetadata{}).Serialize()
	_, _, _, _, err = s.deobfuscateAsFabtokenType(rawFabTok, rawFabMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get recipients")
}

func TestTokensService_DeserializeToken_Extra(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	logger := logging.MustGetLogger("test")
	s, err := NewTokensService(logger, ppm, deserializer)
	require.NoError(t, err)

	// deserializeTokenWithOutputTokenFormat fails at getOutput
	_, _, err = s.deserializeTokenWithOutputTokenFormat(context.Background(), []byte("invalid"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed getting token output")

	// deserializeTokenWithOutputTokenFormat fails at metadata.Deserialize
	tok := &Token{Owner: []byte("owner"), Data: c.GenG1}
	rawTok, _ := tok.Serialize()
	_, _, err = s.deserializeTokenWithOutputTokenFormat(context.Background(), rawTok, []byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize token metadata")

	// Upgrade fails at ParseFabtokenToken
	fabFormat, _ := v1.SupportedTokenFormat(64)
	s.SupportedTokenFormatList = append(s.SupportedTokenFormatList, fabFormat)
	Precisions[fabFormat] = 64
	_, _, _, err = s.DeserializeToken(context.Background(), fabFormat, []byte("invalid"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal fabtoken token")

	// Upgrade fails at GetTokensWithWitness (curve nil) - this is hard to trigger without modifying s.PublicParametersManager
}

func TestNewTokensService_Precision32(t *testing.T) {
	curve := math.BN254
	c := math.Curves[curve]
	rand, err := c.Rand()
	require.NoError(t, err)
	pp := &noghv1.PublicParams{
		Curve: curve,
		PedersenGenerators: []*math.G1{
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
			c.GenG1.Mul(c.NewRandomZr(rand)),
		},
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 32,
		},
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	logger := logging.MustGetLogger("test")

	s, err := NewTokensService(logger, ppm, deserializer)
	require.NoError(t, err)
	assert.NotNil(t, s)

	// verify that 64-bit precision is NOT supported
	found := false
	for _, f := range s.SupportedTokenFormatList {
		// we don't know the exact format string easily, but we know what shouldn't be there
		// Actually, we can check by precision
		p, ok := Precisions[f]
		if ok && p == 64 {
			found = true

			break
		}
	}
	assert.False(t, found)
	// Note: Precisions map also contains fabtoken formats.
	// Let's check SupportedTokenFormat for 64
	format64, _ := SupportedTokenFormat(pp, 64)
	assert.NotContains(t, s.SupportedTokenFormatList, format64)

	fabFormat64, _ := v1.SupportedTokenFormat(64)
	assert.NotContains(t, s.SupportedTokenFormatList, fabFormat64)
}

func TestTokensService_Recipients_DeserializeError(t *testing.T) {
	logger := logging.MustGetLogger("test")
	pp := &noghv1.PublicParams{
		Curve: math.BN254,
		RangeProofParams: &noghv1.RangeProofParams{
			BitLength: 64,
		},
	}
	ppm := &mockPPM{pp: pp}
	deserializer := &mock.Deserializer{}
	s, _ := NewTokensService(logger, ppm, deserializer)

	_, err := s.Recipients([]byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize token")
}
