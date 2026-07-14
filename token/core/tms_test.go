/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/mock"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTMSProvider verifies the functionality of the TMSProvider, including service creation,
// caching, updates, and public parameter retrieval from various sources.
func TestTMSProvider(t *testing.T) {
	tempDir := t.TempDir()
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	identifier := "test.v1"
	ppJSON, err := json.Marshal(&pp.PublicParameters{
		Identifier: identifier,
	})
	require.NoError(t, err)

	ppPath := filepath.Join(tempDir, "pp.bin")
	err = os.WriteFile(ppPath, ppJSON, 0644)
	require.NoError(t, err)

	configService := &mock.ConfigService{}
	pps := &mock.PublicParametersStorage{}

	driverMock := &drivermock.Driver{}
	tokenDriverService := core.NewTokenDriverService([]core.NamedFactory[driver.Driver]{
		{
			Name:   core.TokenDriverIdentifier(identifier),
			Driver: driverMock,
		},
	})

	provider := core.NewTMSProvider(configService, pps, tokenDriverService)

	opts := driver.ServiceOptions{
		Network:   "n1",
		Channel:   "c1",
		Namespace: "ns1",
	}

	expectedPP := &drivermock.PublicParameters{}
	expectedPP.TokenDriverNameReturns("test")
	expectedPP.TokenDriverVersionReturns(1)
	driverMock.PublicParametersFromBytesReturns(expectedPP, nil)

	expectedTMS := &drivermock.TokenManagerService{}
	driverMock.NewTokenServiceReturns(expectedTMS, nil)

	// Test case: GetTokenManagerService handles service creation and caching.
	t.Run("GetTokenManagerService", func(t *testing.T) {
		// Test GetTokenManagerService with opts.PublicParams
		opts.PublicParams = ppJSON
		tms, err := provider.GetTokenManagerService(opts)
		require.NoError(t, err)
		assert.Equal(t, expectedTMS, tms)

		// Test caching: Subsequent calls should return the same instance.
		tms2, err := provider.GetTokenManagerService(opts)
		require.NoError(t, err)
		assert.Equal(t, tms, tms2)

		// Test error when getTokenManagerService fails (e.g. driver not found)
		opts2 := driver.ServiceOptions{Network: "new", Namespace: "ns"}
		ppJSONUnknown, _ := json.Marshal(&pp.PublicParameters{Identifier: "unknown"})
		opts2.PublicParams = ppJSONUnknown
		_, err = provider.GetTokenManagerService(opts2)
		require.Error(t, err)
	})

	// Test case: NewTokenManagerService creates a new instance without caching.
	t.Run("NewTokenManagerService", func(t *testing.T) {
		tms, err := provider.NewTokenManagerService(opts)
		require.NoError(t, err)
		assert.Equal(t, expectedTMS, tms)

		// Test error case
		opts2 := driver.ServiceOptions{Network: "new", Namespace: "ns"}
		ppJSONUnknown, _ := json.Marshal(&pp.PublicParameters{Identifier: "unknown"})
		opts2.PublicParams = ppJSONUnknown
		_, err = provider.NewTokenManagerService(opts2)
		require.Error(t, err)
	})

	// Test case: Update handles updating public parameters and reloading the service.
	t.Run("Update", func(t *testing.T) {
		newPPJSON, _ := json.Marshal(&pp.PublicParameters{
			Identifier: identifier,
			Raw:        []byte("new"),
		})
		opts.PublicParams = newPPJSON

		ppm := &drivermock.PublicParamsManager{}
		oldDigest := sha256.Sum256(ppJSON)
		ppm.PublicParamsHashReturns(oldDigest[:])
		expectedTMS.PublicParamsManagerReturns(ppm)

		// If hashes are different, it should update
		err = provider.Update(opts)
		require.NoError(t, err)

		// If hashes are same, no update
		opts.PublicParams = ppJSON
		err = provider.Update(opts)
		require.NoError(t, err)

		// Test Done error: Failure during unloading of the old service.
		expectedTMS.DoneReturns(errors.New("done error"))
		opts.PublicParams = newPPJSON
		err = provider.Update(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "done error")
		expectedTMS.DoneReturns(nil)

		// Test failure to instantiate new service during update.
		opts2 := driver.ServiceOptions{Network: "n1", Channel: "c1", Namespace: "ns1"}
		ppJSONUnknown, _ := json.Marshal(&pp.PublicParameters{Identifier: "unknown"})
		opts2.PublicParams = ppJSONUnknown
		err = provider.Update(opts2)
		require.Error(t, err)
	})

	// Test case: SetCallback verifies that the callback is invoked when a new TMS is created.
	t.Run("SetCallback", func(t *testing.T) {
		callbackCalled := false
		provider.SetCallback(func(tms driver.TokenManagerService, network, channel, namespace string) error {
			callbackCalled = true

			return nil
		})
		opts.Network = "n2" // New network to avoid cache
		opts.PublicParams = ppJSON
		_, err = provider.GetTokenManagerService(opts)
		require.NoError(t, err)
		assert.True(t, callbackCalled)
	})

	// Test case: loadPublicParams verifies retrieval from storage, config, and fetchers.
	t.Run("loadPublicParams", func(t *testing.T) {
		// Test ppFromStorage: Retrieval from public parameters storage.
		t.Run("ppFromStorage", func(t *testing.T) {
			opts.Network = "n3"
			opts.PublicParams = nil
			pps.PublicParamsReturns(ppJSON, nil)
			_, err = provider.GetTokenManagerService(opts)
			require.NoError(t, err)

			// Error case: Storage returns an error.
			opts.Network = "n3-err"
			pps.PublicParamsReturns(nil, errors.New("storage error"))

			// To avoid panic in ppFromConfig, we need to mock it.
			configService.ConfigurationForReturns(nil, errors.New("no config"))

			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)

			// Empty return case: Storage returns empty bytes.
			opts.Network = "n3-empty"
			pps.PublicParamsReturns([]byte{}, nil)
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)
		})

		// Test ppFromConfig: Retrieval from local configuration.
		t.Run("ppFromConfig", func(t *testing.T) {
			opts.Network = "n4"
			opts.PublicParams = nil
			pps.PublicParamsReturns(nil, nil)

			tmsConfig := &drivermock.Configuration{}
			configService.ConfigurationForReturns(tmsConfig, nil)
			tmsConfig.UnmarshalKeyStub = func(key string, rawVal any) error {
				if key == "publicParameters" {
					rawVal.(*core.PublicParameters).Path = ppPath
				}

				return nil
			}
			_, err = provider.GetTokenManagerService(opts)
			require.NoError(t, err)

			// Error case: UnmarshalKey fails.
			opts.Network = "n4-err1"
			tmsConfig.UnmarshalKeyReturns(errors.New("unmarshal error"))
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)
			tmsConfig.UnmarshalKeyReturns(nil)

			// Error case: ReadFile fails (e.g. path does not exist).
			opts.Network = "n4-err2"
			tmsConfig.UnmarshalKeyStub = func(key string, rawVal any) error {
				if key == "publicParameters" {
					rawVal.(*core.PublicParameters).Path = "non-existent"
				}

				return nil
			}
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)

			// Error case: Path is empty in configuration.
			opts.Network = "n4-empty"
			tmsConfig.UnmarshalKeyStub = func(key string, rawVal any) error {
				if key == "publicParameters" {
					rawVal.(*core.PublicParameters).Path = ""
				}

				return nil
			}
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)
		})

		// Test ppFromFetcher: Retrieval from a public parameters fetcher.
		t.Run("ppFromFetcher", func(t *testing.T) {
			opts.Network = "n5"
			opts.PublicParams = nil
			pps.PublicParamsReturns(nil, nil)
			configService.ConfigurationForReturns(nil, errors.New("no config"))

			fetcher := &drivermock.PublicParamsFetcher{}
			fetcher.FetchReturns(ppJSON, nil)
			opts.PublicParamsFetcher = fetcher
			_, err = provider.GetTokenManagerService(opts)
			require.NoError(t, err)

			// Error case: Fetcher returns an error.
			opts.Network = "n5-err"
			fetcher.FetchReturns(nil, errors.New("fetch error"))
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)

			// Empty return case: Fetcher returns empty bytes.
			opts.Network = "n5-empty"
			fetcher.FetchReturns([]byte{}, nil)
			_, err = provider.GetTokenManagerService(opts)
			require.Error(t, err)
		})
	})

	// Test case: Errors verifies input validation for network, namespace, and public parameters.
	t.Run("Errors", func(t *testing.T) {
		_, err = provider.GetTokenManagerService(driver.ServiceOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "network not specified")

		_, err = provider.GetTokenManagerService(driver.ServiceOptions{Network: "n"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace not specified")

		// Test Update Errors
		require.Error(t, provider.Update(driver.ServiceOptions{}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n"}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n", Namespace: "ns"}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n", Namespace: "ns", PublicParams: nil}))
	})
}
