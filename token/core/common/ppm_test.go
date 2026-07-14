/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPPDeserializer struct {
	deserializeFunc func(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error)
}

func (m *mockPPDeserializer) DeserializePublicParams(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error) {
	return m.deserializeFunc(raw, name, version)
}

func TestPublicParamsManager(t *testing.T) {
	driverName := driver.TokenDriverName("test-driver")
	driverVersion := driver.TokenDriverVersion(1)
	ppRaw := []byte("test-pp-raw")

	t.Run("NewPublicParamsManager_Success", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(nil)
		pp.IssuersReturns([]driver.Identity{driver.Identity("issuer1")})

		deserializer := &mockPPDeserializer{
			deserializeFunc: func(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error) {
				assert.Equal(t, ppRaw, raw)
				assert.Equal(t, driverName, name)
				assert.Equal(t, driverVersion, version)

				return pp, nil
			},
		}

		ppm, err := NewPublicParamsManager[driver.PublicParameters](deserializer, driverName, driverVersion, ppRaw)
		require.NoError(t, err)
		assert.NotNil(t, ppm)
		assert.Equal(t, pp, ppm.PublicParameters())
		assert.Equal(t, pp, ppm.PublicParams())
		assert.NotNil(t, ppm.PublicParamsHash())

		_, _, err = ppm.NewCertifierKeyPair()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported")
	})

	t.Run("NewPublicParamsManager_EmptyRaw", func(t *testing.T) {
		ppm, err := NewPublicParamsManager[driver.PublicParameters](nil, driverName, driverVersion, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty public parameters")
		assert.Nil(t, ppm)
	})

	t.Run("NewPublicParamsManager_DeserializationError", func(t *testing.T) {
		deserializer := &mockPPDeserializer{
			deserializeFunc: func(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error) {
				return nil, errors.New("deserialization failed")
			},
		}
		ppm, err := NewPublicParamsManager[driver.PublicParameters](deserializer, driverName, driverVersion, ppRaw)
		require.Error(t, err)
		assert.Equal(t, "deserialization failed", err.Error())
		assert.Nil(t, ppm)
	})

	t.Run("NewPublicParamsManager_ValidationError", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(errors.New("validation failed"))
		deserializer := &mockPPDeserializer{
			deserializeFunc: func(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error) {
				return pp, nil
			},
		}
		ppm, err := NewPublicParamsManager[driver.PublicParameters](deserializer, driverName, driverVersion, ppRaw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid public parameters: validation failed")
		assert.Nil(t, ppm)
	})

	t.Run("NewPublicParamsManager_NoIssuers", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(nil)
		pp.IssuersReturns(nil)
		deserializer := &mockPPDeserializer{
			deserializeFunc: func(raw []byte, name driver.TokenDriverName, version driver.TokenDriverVersion) (driver.PublicParameters, error) {
				return pp, nil
			},
		}
		ppm, err := NewPublicParamsManager[driver.PublicParameters](deserializer, driverName, driverVersion, ppRaw)
		require.NoError(t, err)
		assert.NotNil(t, ppm)
	})

	t.Run("NewPublicParamsManagerFromParams_Success", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(nil)
		pp.IssuersReturns([]driver.Identity{driver.Identity("issuer1")})

		ppm, err := NewPublicParamsManagerFromParams[driver.PublicParameters](pp)
		require.NoError(t, err)
		assert.NotNil(t, ppm)
		assert.Equal(t, pp, ppm.PublicParameters())
	})

	t.Run("NewPublicParamsManagerFromParams_ValidationError", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(errors.New("validation failed"))

		ppm, err := NewPublicParamsManagerFromParams[driver.PublicParameters](pp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid public parameters: validation failed")
		assert.Nil(t, ppm)
	})

	t.Run("NewPublicParamsManagerFromParams_NoIssuers", func(t *testing.T) {
		pp := &mock.PublicParameters{}
		pp.ValidateReturns(nil)
		pp.IssuersReturns(nil)

		ppm, err := NewPublicParamsManagerFromParams[driver.PublicParameters](pp)
		require.NoError(t, err)
		assert.NotNil(t, ppm)
	})
}
