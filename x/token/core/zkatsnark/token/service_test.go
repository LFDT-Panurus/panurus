/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
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

func TestTokensService_Deobfuscate_Success(t *testing.T) {
	// Create a note and compute its commitment so we can build a matching output.
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)
	note.Issuer = []byte("issuer-1")

	cm, err := note.Commitment()
	require.NoError(t, err)
	cmBytes := cm.Bytes()

	// Build a matching OutputDescription.
	outputDesc := snarktoken.OutputDescription{
		CommitmentOut: cmBytes[:],
		Recipient:     []byte("alice"),
	}
	outputRaw, err := json.Marshal(outputDesc)
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
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	metadata, err := note.Serialize()
	require.NoError(t, err)

	deser := &stubDeserializer{}

	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), nil, deser)
	require.NoError(t, err)

	_, _, _, _, err = svc.Deobfuscate(context.Background(), []byte("not json"), metadata)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal output")
}

func TestTokensService_Deobfuscate_CommitmentMismatch(t *testing.T) {
	note, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)

	metadata, err := note.Serialize()
	require.NoError(t, err)

	// Build an output with a different commitment.
	outputDesc := snarktoken.OutputDescription{
		CommitmentOut: make([]byte, 32), // zeroes won't match
		Recipient:     []byte("alice"),
	}
	outputRaw, err := json.Marshal(outputDesc)
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
	outputRaw, err := json.Marshal(outputDesc)
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

	_, err = svc.Recipients([]byte("not json"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal output")
}
