/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"testing"

	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVaultLedgerTokenAndMetadataLoaderWithCounterfeiter(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	ctx := context.Background()

	t.Run("LoadTokens_Success", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		outputs := [][]byte{[]byte("output1")}
		metadata := [][]byte{[]byte("meta1")}
		formats := []token.Format{token.Format("f1")}
		vault.GetTokenOutputsAndMetaReturns(outputs, metadata, formats, nil)
		deserializer.DeserializeTokenReturns("tok1", nil)
		deserializer.DeserializeMetadataReturns("meta1-des", nil)

		res, err := loader.LoadTokens(ctx, ids)
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Equal(t, "tok1", res[0].Token)
		assert.Equal(t, "meta1-des", res[0].Metadata)
	})

	t.Run("LoadTokens_VaultError", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		vault.GetTokenOutputsAndMetaReturns(nil, nil, nil, errors.New("vault error"))
		res, err := loader.LoadTokens(ctx, ids)
		require.Error(t, err)
		assert.Equal(t, "vault error", err.Error())
		assert.Nil(t, res)
	})

	t.Run("LoadTokens_EmptyOutput", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		outputs := [][]byte{nil}
		metadata := [][]byte{[]byte("meta1")}
		formats := []token.Format{token.Format("f1")}
		vault.GetTokenOutputsAndMetaReturns(outputs, metadata, formats, nil)
		_, err := loader.LoadTokens(ctx, ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil comm value")
	})

	t.Run("LoadTokens_EmptyMetadata", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		outputs := [][]byte{[]byte("output1")}
		metadata := [][]byte{nil}
		formats := []token.Format{token.Format("f1")}
		vault.GetTokenOutputsAndMetaReturns(outputs, metadata, formats, nil)
		_, err := loader.LoadTokens(ctx, ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil info value")
	})

	t.Run("LoadTokens_DeserializeTokenError", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		outputs := [][]byte{[]byte("output1")}
		metadata := [][]byte{[]byte("meta1")}
		formats := []token.Format{token.Format("f1")}
		vault.GetTokenOutputsAndMetaReturns(outputs, metadata, formats, nil)
		deserializer.DeserializeTokenReturns("", errors.New("bad token"))

		_, err := loader.LoadTokens(ctx, ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad token")
	})

	t.Run("LoadTokens_DeserializeMetadataError", func(t *testing.T) {
		vault := &dmock.TokenVault{}
		deserializer := &dmock.TokenAndMetadataDeserializer[string, string]{}
		loader := NewVaultLedgerTokenAndMetadataLoader[string, string](vault, deserializer)
		outputs := [][]byte{[]byte("output1")}
		metadata := [][]byte{[]byte("meta1")}
		formats := []token.Format{token.Format("f1")}
		vault.GetTokenOutputsAndMetaReturns(outputs, metadata, formats, nil)
		deserializer.DeserializeTokenReturns("tok1", nil)
		deserializer.DeserializeMetadataReturns("", errors.New("bad meta"))

		_, err := loader.LoadTokens(ctx, ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad meta")
	})
}

func TestVaultTokenInfoLoaderWithCounterfeiter(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	ctx := context.Background()

	t.Run("GetTokenInfos_Success", func(t *testing.T) {
		qe := &dmock.QueryEngine{}
		deserializer := &dmock.MetadataDeserializer[string]{}
		loader := NewVaultTokenInfoLoader[string](qe, deserializer)
		metadata := [][]byte{[]byte("meta1")}
		qe.GetTokenMetadataReturns(metadata, nil)
		deserializer.DeserializeMetadataReturns("meta1-des", nil)

		res, err := loader.GetTokenInfos(ctx, ids)
		require.NoError(t, err)
		assert.Equal(t, []string{"meta1-des"}, res)
	})

	t.Run("GetTokenInfos_QueryError", func(t *testing.T) {
		qe := &dmock.QueryEngine{}
		deserializer := &dmock.MetadataDeserializer[string]{}
		loader := NewVaultTokenInfoLoader[string](qe, deserializer)
		qe.GetTokenMetadataReturns(nil, errors.New("query error"))
		res, err := loader.GetTokenInfos(ctx, ids)
		require.Error(t, err)
		assert.Equal(t, "query error", err.Error())
		assert.Nil(t, res)
	})

	t.Run("GetTokenInfos_DeserializeError", func(t *testing.T) {
		qe := &dmock.QueryEngine{}
		deserializer := &dmock.MetadataDeserializer[string]{}
		loader := NewVaultTokenInfoLoader[string](qe, deserializer)
		metadata := [][]byte{[]byte("meta1")}
		qe.GetTokenMetadataReturns(metadata, nil)
		deserializer.DeserializeMetadataReturns("", errors.New("bad meta"))

		_, err := loader.GetTokenInfos(ctx, ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad meta")
	})
}

func TestVaultTokenLoaderWithCounterfeiter(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	ctx := context.Background()

	t.Run("GetTokens_Success", func(t *testing.T) {
		qe := &dmock.QueryEngine{}
		loader := NewVaultTokenLoader(qe)
		tokens := []*token.Token{{Owner: []byte("owner1")}}
		qe.GetTokensReturns(tokens, nil)

		res, err := loader.GetTokens(ctx, ids)
		require.NoError(t, err)
		assert.Equal(t, tokens, res)
	})
}

func TestVaultTokenCertificationLoaderWithCounterfeiter(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	ctx := context.Background()

	t.Run("GetCertifications_Success", func(t *testing.T) {
		storage := &dmock.TokenCertificationStorage{}
		loader := &VaultTokenCertificationLoader{TokenCertificationStorage: storage}
		certs := [][]byte{[]byte("cert1")}
		storage.GetCertificationsReturns(certs, nil)

		res, err := loader.GetCertifications(ctx, ids)
		require.NoError(t, err)
		assert.Equal(t, certs, res)

		storage.GetCertificationsReturns(nil, errors.New("cert error"))
		res, err = loader.GetCertifications(ctx, ids)
		require.Error(t, err)
		assert.Equal(t, "cert error", err.Error())
		assert.Nil(t, res)
	})
}

func TestIdentityTokenAndMetadataDeserializerWithCounterfeiter(t *testing.T) {
	d := IdentityTokenAndMetadataDeserializer{}

	b := []byte("hello")
	res, err := d.DeserializeToken(b)
	require.NoError(t, err)
	assert.Equal(t, b, res)

	res, err = d.DeserializeMetadata(b)
	require.NoError(t, err)
	assert.Equal(t, b, res)
}
