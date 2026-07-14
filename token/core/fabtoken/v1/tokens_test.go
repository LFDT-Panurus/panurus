/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"context"
	"testing"

	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokensService(t *testing.T) {
	pp, err := setup.NewWith("fabtoken", 1, 64)
	require.NoError(t, err)

	deserializer := &mock.Deserializer{}
	service, err := v1.NewTokensService(pp, deserializer)
	require.NoError(t, err)

	t.Run("Recipients", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		service, err := v1.NewTokensService(pp, deserializer)
		require.NoError(t, err)

		// Success
		tok := &actions.Output{
			Owner:    []byte("owner"),
			Type:     "type",
			Quantity: "100",
		}
		rawTok, err := tok.Serialize()
		require.NoError(t, err)

		expectedRecipient := driver.Identity([]byte("recipient"))
		deserializer.RecipientsReturns([]driver.Identity{expectedRecipient}, nil)
		recipients, err := service.Recipients(rawTok)
		require.NoError(t, err)
		assert.Equal(t, []driver.Identity{expectedRecipient}, recipients)

		// Error: Deserialize failure
		_, err = service.Recipients([]byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling token")

		// Error: Recipients failure
		deserializer.RecipientsReturns(nil, assert.AnError)
		_, err = service.Recipients(rawTok)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recipients")
	})

	t.Run("Deobfuscate", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		service, err := v1.NewTokensService(pp, deserializer)
		require.NoError(t, err)

		tok := &actions.Output{
			Owner:    []byte("owner"),
			Type:     "type",
			Quantity: "100",
		}
		rawTok, err := tok.Serialize()
		require.NoError(t, err)

		meta := &actions.OutputMetadata{
			Issuer: []byte("issuer"),
		}
		rawMeta, err := meta.Serialize()
		require.NoError(t, err)

		// Success
		expectedRecipient := driver.Identity([]byte("recipient"))
		deserializer.RecipientsReturns([]driver.Identity{expectedRecipient}, nil)
		tokOut, issuer, recipients, format, err := service.Deobfuscate(context.Background(), rawTok, rawMeta)
		require.NoError(t, err)
		assert.Equal(t, tok.Owner, tokOut.Owner)
		assert.Equal(t, tok.Type, tokOut.Type)
		assert.Equal(t, tok.Quantity, tokOut.Quantity)
		assert.Equal(t, driver.Identity(meta.Issuer), issuer)
		assert.Equal(t, []driver.Identity{expectedRecipient}, recipients)
		assert.NotEmpty(t, format)

		// Error: Deserialize token failure
		_, _, _, _, err = service.Deobfuscate(context.Background(), []byte("invalid"), rawMeta)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling token")

		// Error: Deserialize metadata failure
		_, _, _, _, err = service.Deobfuscate(context.Background(), rawTok, []byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling token information")

		// Error: Recipients failure
		deserializer.RecipientsReturns(nil, assert.AnError)
		_, _, _, _, err = service.Deobfuscate(context.Background(), rawTok, rawMeta)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recipients")
	})

	t.Run("SupportedTokenFormats", func(t *testing.T) {
		formats := service.SupportedTokenFormats()
		assert.Len(t, formats, 1)
		assert.NotEmpty(t, formats[0])
	})
}

func TestTokensUpgradeService(t *testing.T) {
	service := &v1.TokensUpgradeService{}

	ch, err := service.NewUpgradeChallenge()
	require.Error(t, err)
	assert.Nil(t, ch)

	proof, err := service.GenUpgradeProof(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Nil(t, proof)

	ok, err := service.CheckUpgradeProof(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.False(t, ok)
}

func TestSupportedTokenFormat(t *testing.T) {
	format, err := v1.SupportedTokenFormat(64)
	require.NoError(t, err)
	assert.NotEmpty(t, format)

	format2, err := v1.SupportedTokenFormat(32)
	require.NoError(t, err)
	assert.NotEmpty(t, format2)
	assert.NotEqual(t, format, format2)
}
