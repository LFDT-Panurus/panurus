/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"context"
	"slices"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// ── mock deserializer ─────────────────────────────────────────────────────────

type stubDeserializer struct {
	recipientsResult []driver.Identity
	recipientsErr    error
}

func (s *stubDeserializer) GetIssuerVerifier(context.Context, driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (s *stubDeserializer) GetOwnerVerifier(context.Context, driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (s *stubDeserializer) GetAuditorVerifier(context.Context, driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (s *stubDeserializer) GetOwnerMatcher(context.Context, driver.Identity) (driver.Matcher, error) {
	return nil, nil
}

func (s *stubDeserializer) GetAuditInfo(context.Context, driver.Identity, driver.AuditInfoProvider) ([]byte, error) {
	return nil, nil
}

func (s *stubDeserializer) GetAuditInfoMatcher(context.Context, driver.Identity, []byte) (driver.Matcher, error) {
	return nil, nil
}

func (s *stubDeserializer) Recipients(id driver.Identity) ([]driver.Identity, error) {
	return s.recipientsResult, s.recipientsErr
}

func (s *stubDeserializer) MatchIdentity(context.Context, driver.Identity, []byte) error {
	return nil
}

func TestNewTokensService(t *testing.T) {
	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, &stubDeserializer{})
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestTokensService_SupportedTokenFormats(t *testing.T) {
	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, &stubDeserializer{})
	require.NoError(t, err)

	formats := svc.SupportedTokenFormats()
	require.Len(t, formats, 1)
	require.Equal(t, "zkatsnark", string(formats[0]))
}

// TestTokensService_InPlaceFabtokenFormats pins the split between the two
// fabtoken upgrade paths: precisions within min(PrecisionValue, MaxBits) are
// spent in place, the others are left to burn-and-re-issue, never both.
func TestTokensService_InPlaceFabtokenFormats(t *testing.T) {
	for _, tc := range []struct {
		precision uint64
		maxBits   int
		inPlace   []uint64
	}{
		{precision: 64, maxBits: 64, inPlace: []uint64{16, 32, 64}},
		{precision: 64, maxBits: 32, inPlace: []uint64{16, 32}},
		{precision: 16, maxBits: 64, inPlace: []uint64{16}},
		{precision: 8, maxBits: 64, inPlace: nil},
	} {
		p := &pp.PublicParams{PrecisionValue: tc.precision, MaxBits: tc.maxBits}
		require.Equal(t, tc.inPlace, snarktoken.InPlaceFabtokenPrecisions(p))

		svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), &staticPPM{pp: p}, &stubDeserializer{})
		require.NoError(t, err)
		up, err := snarktoken.NewTokensUpgradeService(nil, tc.precision, nil, nil, svc.SupportedTokenFormats())
		require.NoError(t, err)
		for _, precision := range []uint64{16, 32, 64} {
			format, err := fabtoken.SupportedTokenFormat(precision)
			require.NoError(t, err)
			direct := slices.Contains(svc.SupportedTokenFormats(), format)
			upgradable := slices.Contains(up.UpgradeSupportedTokenFormatList, format)
			require.NotEqual(t, direct, upgradable, "precision [%d] must take exactly one path", precision)
			require.Equal(t, slices.Contains(tc.inPlace, precision), direct)
		}
	}
}

// TestTokensService_DeobfuscateFabtoken checks that a legacy fabtoken output
// held by a zkatsnark node is opened from its cleartext content.
func TestTokensService_DeobfuscateFabtoken(t *testing.T) {
	raw, err := (&actions.Output{Owner: []byte("alice"), Type: "USD", Quantity: "0x10"}).Serialize()
	require.NoError(t, err)
	md, err := (&actions.OutputMetadata{Issuer: []byte("issuer")}).Serialize()
	require.NoError(t, err)
	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, &stubDeserializer{})
	require.NoError(t, err)

	tok, issuer, _, format, err := svc.Deobfuscate(t.Context(), raw, md)
	require.NoError(t, err)
	require.Equal(t, "USD", string(tok.Type))
	require.Equal(t, "0x10", tok.Quantity)
	require.Equal(t, []byte("alice"), tok.Owner)
	require.Equal(t, []byte("issuer"), []byte(issuer))
	require.Equal(t, snarktoken.OutputTokenFormat, format)

	// as in zkatdlog, metadata that does not parse is refused, empty included
	for _, bad := range [][]byte{nil, []byte("bad")} {
		_, _, _, _, err = svc.Deobfuscate(t.Context(), raw, bad)
		require.ErrorContains(t, err, "failed to unmarshal fabtoken output metadata")
	}
}

type staticPPM struct {
	pp *pp.PublicParams
}

func (s *staticPPM) PublicParams() *pp.PublicParams { return s.pp }

func (s *staticPPM) PublicParameters() driver.PublicParameters { return s.pp }

func (s *staticPPM) NewCertifierKeyPair() ([]byte, []byte, error) { return nil, nil, nil }

func (s *staticPPM) PublicParamsHash() driver.PPHash { return nil }

func TestTokensService_Deobfuscate_Success(t *testing.T) {
	// Create a note and compute its commitment so we can build a matching output.
	note, err := snarktoken.NewRandomNote(100, "USD", ecc.BLS12_381)
	require.NoError(t, err)
	note.Issuer = []byte("issuer-1")

	cm, err := note.Commitment(ecc.BLS12_381)
	require.NoError(t, err)

	// Build a matching OutputDescription.
	outputDesc := snarktoken.OutputDescription{
		CommitmentOut: cm,
		Recipient:     []byte("alice"),
	}
	outputRaw, err := outputDesc.Serialize()
	require.NoError(t, err)

	// Serialize the note as metadata.
	metadata, err := note.Serialize()
	require.NoError(t, err)

	deser := &stubDeserializer{
		recipientsResult: []driver.Identity{[]byte("alice")},
	}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	tok, issuer, recipients, format, err := svc.Deobfuscate(context.Background(), outputRaw, metadata)
	require.NoError(t, err)
	require.Equal(t, "zkatsnark", string(format))
	require.Equal(t, "USD", string(tok.Type))
	require.Equal(t, []byte("alice"), tok.Owner)
	require.Equal(t, []byte("issuer-1"), []byte(issuer))
	require.Len(t, recipients, 1)
}

func TestTokensService_Deobfuscate_BadMetadata(t *testing.T) {
	deser := &stubDeserializer{}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	_, _, _, _, err = svc.Deobfuscate(context.Background(), []byte("{}"), []byte("bad"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to deserialize token metadata")
}

func TestTokensService_Deobfuscate_BadOutput(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD", ecc.BLS12_381)
	require.NoError(t, err)

	metadata, err := note.Serialize()
	require.NoError(t, err)

	deser := &stubDeserializer{}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	_, _, _, _, err = svc.Deobfuscate(context.Background(), []byte("bad data"), metadata)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal output")
}

func TestTokensService_Deobfuscate_CommitmentMismatch(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD", ecc.BLS12_381)
	require.NoError(t, err)

	metadata, err := note.Serialize()
	require.NoError(t, err)

	// Build an output with a different commitment.
	outputDesc := snarktoken.OutputDescription{
		CommitmentOut: make([]byte, 32), // zeroes won't match
		Recipient:     []byte("alice"),
	}
	outputRaw, err := outputDesc.Serialize()
	require.NoError(t, err)

	deser := &stubDeserializer{}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	_, _, _, _, err = svc.Deobfuscate(context.Background(), outputRaw, metadata)
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata commitment does not match output commitment")
}

func TestTokensService_Recipients_Success(t *testing.T) {
	outputDesc := snarktoken.OutputDescription{
		Recipient: []byte("bob"),
	}
	outputRaw, err := outputDesc.Serialize()
	require.NoError(t, err)

	deser := &stubDeserializer{
		recipientsResult: []driver.Identity{[]byte("bob")},
	}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	r, err := svc.Recipients(outputRaw)
	require.NoError(t, err)
	require.Len(t, r, 1)
	require.Equal(t, []byte("bob"), []byte(r[0]))
}

func TestTokensService_Recipients_BadJSON(t *testing.T) {
	deser := &stubDeserializer{}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	_, err = svc.Recipients([]byte("bad data"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal output")
}
