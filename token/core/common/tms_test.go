/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPPM[T driver.PublicParameters] struct {
	dmock.PublicParamsManager
}

func (m *mockPPM[T]) PublicParams() T {
	pp := m.PublicParameters()
	if pp == nil {
		var zero T

		return zero
	}

	return pp.(T)
}

func TestServiceWithCounterfeiter(t *testing.T) {
	logger := &logging.MockLogger{}
	ppm := &mockPPM[driver.PublicParameters]{}
	ip := &dmock.IdentityProvider{}
	des := &dmock.Deserializer{}
	config := &dmock.Configuration{}
	cert := &dmock.CertificationService{}
	issue := &dmock.IssueService{}
	transfer := &dmock.TransferService{}
	auditor := &dmock.AuditorService{}
	tokens := &dmock.TokensService{}
	tokensUpgrade := &dmock.TokensUpgradeService{}
	auth := &dmock.Authorization{}
	val := &dmock.Validator{}

	t.Run("GetterTests", func(t *testing.T) {
		s, err := NewTokenService[driver.PublicParameters](
			logger,
			nil, // driver.WalletService
			ppm,
			ip,
			des,
			config,
			cert,
			issue,
			transfer,
			auditor,
			tokens,
			tokensUpgrade,
			auth,
			val,
		)
		require.NoError(t, err)
		assert.NotNil(t, s)

		assert.Equal(t, ip, s.IdentityProvider())
		assert.Equal(t, des, s.Deserializer())
		assert.Equal(t, cert, s.CertificationService())
		assert.Equal(t, ppm, s.PublicParamsManager())
		assert.Equal(t, config, s.Configuration())
		assert.Nil(t, s.WalletService())
		assert.Equal(t, issue, s.IssueService())
		assert.Equal(t, transfer, s.TransferService())
		assert.Equal(t, auditor, s.AuditorService())
		assert.Equal(t, tokens, s.TokensService())
		assert.Equal(t, tokensUpgrade, s.TokensUpgradeService())
		assert.Equal(t, auth, s.Authorization())

		v, err := s.Validator()
		require.NoError(t, err)
		assert.Equal(t, val, v)

		require.NoError(t, s.Done())
	})
}
