/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeserializerWithCounterfeiter(t *testing.T) {
	ctx := context.Background()
	id := driver.Identity("test-id")
	auditInfo := []byte("audit-info")
	expectedErr := errors.New("mock error")

	mvAuditor := &dmock.VerifierDeserializer{}
	mvOwner := &dmock.VerifierDeserializer{}
	mvIssuer := &dmock.VerifierDeserializer{}
	mAMP := &dmock.AuditMatcherProvider{}
	mRE := &dmock.RecipientExtractor{}

	d := NewDeserializer(mvAuditor, mvOwner, mvIssuer, mAMP, mRE)
	assert.NotNil(t, d)

	t.Run("GetOwnerVerifier", func(t *testing.T) {
		verifier := &dmock.Verifier{}
		mvOwner.DeserializeVerifierReturns(verifier, nil)
		res, err := d.GetOwnerVerifier(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, verifier, res)

		mvOwner.DeserializeVerifierReturns(nil, expectedErr)
		res, err = d.GetOwnerVerifier(ctx, id)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})

	t.Run("GetIssuerVerifier", func(t *testing.T) {
		verifier := &dmock.Verifier{}
		mvIssuer.DeserializeVerifierReturns(verifier, nil)
		res, err := d.GetIssuerVerifier(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, verifier, res)

		mvIssuer.DeserializeVerifierReturns(nil, expectedErr)
		res, err = d.GetIssuerVerifier(ctx, id)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})

	t.Run("GetAuditorVerifier", func(t *testing.T) {
		verifier := &dmock.Verifier{}
		mvAuditor.DeserializeVerifierReturns(verifier, nil)
		res, err := d.GetAuditorVerifier(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, verifier, res)

		mvAuditor.DeserializeVerifierReturns(nil, expectedErr)
		res, err = d.GetAuditorVerifier(ctx, id)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})

	t.Run("Recipients", func(t *testing.T) {
		recipients := []driver.Identity{driver.Identity("r1"), driver.Identity("r2")}
		mRE.RecipientsReturns(recipients, nil)
		res, err := d.Recipients(id)
		require.NoError(t, err)
		assert.Equal(t, recipients, res)

		mRE.RecipientsReturns(nil, expectedErr)
		res, err = d.Recipients(id)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})

	t.Run("GetAuditInfoMatcher", func(t *testing.T) {
		matcher := &dmock.Matcher{}
		mAMP.GetAuditInfoMatcherReturns(matcher, nil)
		res, err := d.GetAuditInfoMatcher(ctx, id, auditInfo)
		require.NoError(t, err)
		assert.Equal(t, matcher, res)

		mAMP.GetAuditInfoMatcherReturns(nil, expectedErr)
		res, err = d.GetAuditInfoMatcher(ctx, id, auditInfo)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})

	t.Run("MatchIdentity", func(t *testing.T) {
		mAMP.MatchIdentityReturns(nil)
		err := d.MatchIdentity(ctx, id, auditInfo)
		require.NoError(t, err)

		mAMP.MatchIdentityReturns(expectedErr)
		err = d.MatchIdentity(ctx, id, auditInfo)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("GetAuditInfo", func(t *testing.T) {
		aip := &dmock.AuditInfoProvider{}
		mAMP.GetAuditInfoReturns(auditInfo, nil)
		res, err := d.GetAuditInfo(ctx, id, aip)
		require.NoError(t, err)
		assert.Equal(t, auditInfo, res)

		mAMP.GetAuditInfoReturns(nil, expectedErr)
		res, err = d.GetAuditInfo(ctx, id, aip)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, res)
	})
}
