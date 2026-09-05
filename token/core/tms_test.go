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
	"strings"
	"sync"
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
		require.NotErrorIs(t, err, core.ErrTMSNotFound)

		_, err = provider.GetTokenManagerService(driver.ServiceOptions{Network: "n"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace not specified")
		require.NotErrorIs(t, err, core.ErrTMSNotFound)

		// Test Update Errors
		require.Error(t, provider.Update(driver.ServiceOptions{}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n"}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n", Namespace: "ns"}))
		require.Error(t, provider.Update(driver.ServiceOptions{Network: "n", Namespace: "ns", PublicParams: nil}))
	})

	// Test case: ErrTMSNotFound is returned (and distinguishable via errors.Is) when no public
	// parameters can be retrieved from any source, but not for unrelated input-validation errors.
	t.Run("ErrTMSNotFound", func(t *testing.T) {
		pps.PublicParamsReturns(nil, errors.New("storage error"))
		configService.ConfigurationForReturns(nil, errors.New("no config"))

		_, err = provider.GetTokenManagerService(driver.ServiceOptions{Network: "n6", Namespace: "ns6"})
		require.Error(t, err)
		require.ErrorIs(t, err, core.ErrTMSNotFound)

		_, err = provider.NewTokenManagerService(driver.ServiceOptions{Network: "n6-new", Namespace: "ns6"})
		require.Error(t, err)
		require.ErrorIs(t, err, core.ErrTMSNotFound)
	})
}

// ppFallbackContext holds the collaborators of a TMSProvider under test, wired so that every
// public parameters source can be driven independently.
type ppFallbackContext struct {
	provider     *core.TMSProvider
	config       *mock.ConfigService
	tmsConfig    *drivermock.Configuration
	storage      *mock.PublicParametersStorage
	driver       *drivermock.Driver
	fetcher      *drivermock.PublicParamsFetcher
	tms          *drivermock.TokenManagerService
	tempDir      string
	configPPPath string
}

// newPPFallbackContext returns a TMSProvider whose driver accepts the public parameters tagged
// [ok], rejects those tagged [malformed] the way a driver rejects public parameters it cannot
// deserialize, and panics on those tagged [panic]. By default no source holds any public
// parameters: each test case installs the ones it needs.
func newPPFallbackContext(t *testing.T) *ppFallbackContext {
	t.Helper()

	c := &ppFallbackContext{
		config:    &mock.ConfigService{},
		storage:   &mock.PublicParametersStorage{},
		driver:    &drivermock.Driver{},
		fetcher:   &drivermock.PublicParamsFetcher{},
		tms:       &drivermock.TokenManagerService{},
		tempDir:   t.TempDir(),
		tmsConfig: &drivermock.Configuration{},
	}
	c.configPPPath = filepath.Join(c.tempDir, "config-pp.bin")

	c.driver.PublicParametersFromBytesStub = func(raw []byte) (driver.PublicParameters, error) {
		serialized := &pp.PublicParameters{}
		require.NoError(t, json.Unmarshal(raw, serialized))
		switch tag := string(serialized.Raw); {
		case strings.HasPrefix(tag, "malformed"):
			return nil, errors.New("failed to deserialize public parameters")
		case strings.HasPrefix(tag, "panic"):
			panic("invalid curve id")
		}

		publicParams := &drivermock.PublicParameters{}
		publicParams.TokenDriverNameReturns("test")
		publicParams.TokenDriverVersionReturns(1)

		return publicParams, nil
	}
	c.driver.NewTokenServiceReturns(c.tms, nil)

	// no source holds any public parameters, unless a test case says otherwise
	c.storage.PublicParamsReturns(nil, nil)
	c.config.ConfigurationForReturns(nil, errors.New("no configuration"))
	c.fetcher.FetchReturns(nil, nil)

	c.provider = core.NewTMSProvider(c.config, c.storage, core.NewTokenDriverService([]core.NamedFactory[driver.Driver]{
		{Name: core.TokenDriverIdentifier("test.v1"), Driver: c.driver},
	}))

	return c
}

// pubParams returns public parameters for the registered driver, tagged to drive the stubbed
// driver behaviour ("ok", "malformed", "panic") and to distinguish one blob from another.
func (c *ppFallbackContext) pubParams(t *testing.T, tag string) []byte {
	t.Helper()
	raw, err := json.Marshal(&pp.PublicParameters{Identifier: "test.v1", Raw: []byte(tag)})
	require.NoError(t, err)

	return raw
}

// unknownDriverPubParams returns public parameters whose driver identifier is not registered,
// which is what driver name/version skew looks like to the token driver service.
func (c *ppFallbackContext) unknownDriverPubParams(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(&pp.PublicParameters{Identifier: "test.v2"})
	require.NoError(t, err)

	return raw
}

// setConfigPubParams makes the local configuration point at a file holding the passed public
// parameters.
func (c *ppFallbackContext) setConfigPubParams(t *testing.T, ppRaw []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(c.configPPPath, ppRaw, 0600))
	c.tmsConfig.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == "publicParameters" {
			rawVal.(*core.PublicParameters).Path = c.configPPPath
		}

		return nil
	}
	c.config.ConfigurationForReturns(c.tmsConfig, nil)
}

// newPPFallbackOpts returns the service options for the TMS under test.
func newPPFallbackOpts(c *ppFallbackContext, publicParams []byte) driver.ServiceOptions {
	return driver.ServiceOptions{
		Network:             "n1",
		Channel:             "c1",
		Namespace:           "ns1",
		PublicParams:        publicParams,
		PublicParamsFetcher: c.fetcher,
	}
}

// TestTMSProviderPublicParamsFallback verifies that the provider does not commit to the first
// retrievable public parameters, but to the first ones the driver accepts: a source that yields
// unusable public parameters must not shadow the lower-priority sources, and in particular not
// the authoritative fetcher. See https://github.com/LFDT-Panurus/panurus/issues/2281.
func TestTMSProviderPublicParamsFallback(t *testing.T) {
	// Test case: the local storage holds public parameters the driver cannot deserialize; the
	// public parameters fetched from the network are used instead.
	t.Run("unusable storage public params fall back to the fetcher", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns(c.pubParams(t, "malformed"), nil)
		fetched := c.pubParams(t, "ok")
		c.fetcher.FetchReturns(fetched, nil)

		tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
		assert.Equal(t, 1, c.fetcher.FetchCallCount())
		require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
		_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
		assert.Equal(t, fetched, usedPP)
	})

	// Test case: the caller-provided public parameters carry a driver identifier that is not
	// registered (driver name/version skew); the local storage takes over.
	t.Run("unusable options public params fall back to the storage", func(t *testing.T) {
		c := newPPFallbackContext(t)
		stored := c.pubParams(t, "ok")
		c.storage.PublicParamsReturns(stored, nil)

		tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, c.unknownDriverPubParams(t)))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
		assert.Equal(t, 0, c.fetcher.FetchCallCount())
		require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
		_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
		assert.Equal(t, stored, usedPP)
	})

	// Test case: the public parameters on the local filesystem are stale; the public parameters
	// fetched from the network are used instead.
	t.Run("unusable configuration public params fall back to the fetcher", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.setConfigPubParams(t, c.unknownDriverPubParams(t))
		fetched := c.pubParams(t, "ok")
		c.fetcher.FetchReturns(fetched, nil)

		tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
		require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
		_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
		assert.Equal(t, fetched, usedPP)
	})

	// Test case: a driver that panics while deserializing the public parameters of one source
	// does not abort the walk over the remaining sources.
	t.Run("panic on one source falls through to the next", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns(c.pubParams(t, "panic"), nil)
		fetched := c.pubParams(t, "ok")
		c.fetcher.FetchReturns(fetched, nil)

		tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
		require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
		_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
		assert.Equal(t, fetched, usedPP)
	})

	// Test case: public parameters shared by two sources are only tried once.
	t.Run("identical public params are tried once", func(t *testing.T) {
		c := newPPFallbackContext(t)
		stale := c.pubParams(t, "malformed")
		c.storage.PublicParamsReturns(stale, nil)
		c.setConfigPubParams(t, stale)
		c.fetcher.FetchReturns(c.pubParams(t, "ok"), nil)

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		// once for the stale public params shared by storage and configuration, once for the
		// fetched ones
		assert.Equal(t, 2, c.driver.PublicParametersFromBytesCallCount())
	})

	// Test case: every source holds the same unusable public parameters. They are deserialized
	// once, and the error still accounts for every source that was walked, so that a duplicated
	// stale copy is not silently invisible.
	t.Run("identical unusable public params are all reported", func(t *testing.T) {
		c := newPPFallbackContext(t)
		stale := c.pubParams(t, "malformed")
		c.storage.PublicParamsReturns(stale, nil)
		c.setConfigPubParams(t, stale)
		c.fetcher.FetchReturns(stale, nil)

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, stale))
		require.Error(t, err)
		require.NotErrorIs(t, err, core.ErrTMSNotFound)
		assert.Equal(t, 1, c.driver.PublicParametersFromBytesCallCount())
		assert.Contains(t, err.Error(), "cannot instantiate token service with the public params from [options]")
		for _, source := range []string{"storage", "configuration", "fetcher"} {
			assert.Contains(t, err.Error(), "public params from ["+source+"] are identical to those from [options], already tried")
		}
	})

	// Test case: every source holds unusable public parameters. The error reports each source,
	// and it is not an ErrTMSNotFound: the TMS exists, its public parameters are just unusable.
	t.Run("all sources unusable", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns(c.pubParams(t, "malformed-storage"), nil)
		c.setConfigPubParams(t, c.unknownDriverPubParams(t))
		c.fetcher.FetchReturns(c.pubParams(t, "panic-fetcher"), nil)

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, c.pubParams(t, "malformed-options")))
		require.Error(t, err)
		require.NotErrorIs(t, err, core.ErrTMSNotFound)
		assert.Contains(t, err.Error(), "failed to instantiate token service")
		for _, source := range []string{"options", "storage", "configuration", "fetcher"} {
			assert.Contains(t, err.Error(), "from ["+source+"]", "error must report source [%s]", source)
		}
	})

	// Test case: no source holds any public parameters. The TMS has not been set up yet, so the
	// error wraps ErrTMSNotFound and reports why each source came up empty.
	t.Run("no source holds public params", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns(nil, errors.New("storage error"))

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.Error(t, err)
		require.ErrorIs(t, err, core.ErrTMSNotFound)
		assert.Contains(t, err.Error(), "cannot retrieve public params")
		assert.Contains(t, err.Error(), "storage error")
		for _, source := range []string{"options", "storage", "configuration", "fetcher"} {
			assert.Contains(t, err.Error(), "from ["+source+"]", "error must report source [%s]", source)
		}
		assert.Equal(t, 0, c.driver.NewTokenServiceCallCount())
	})

	// Test case: a nil configuration returned with no error is reported, not dereferenced.
	t.Run("nil configuration does not panic", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.config.ConfigurationForReturns(nil, nil)
		fetched := c.pubParams(t, "ok")
		c.fetcher.FetchReturns(fetched, nil)

		tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
	})

	// Test case: the successful public parameters are the ones the service is created with, and
	// a failed resolution is not cached.
	t.Run("failed resolution is not cached", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns(c.pubParams(t, "malformed"), nil)

		opts := newPPFallbackOpts(c, nil)
		_, err := c.provider.GetTokenManagerService(opts)
		require.Error(t, err)

		// the network now serves usable public parameters: the next call must succeed
		c.fetcher.FetchReturns(c.pubParams(t, "ok"), nil)
		tms, err := c.provider.GetTokenManagerService(opts)
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
	})
}

// setSource installs public parameters tagged tag into the named source, or makes the source
// come up empty when tag is "". The tag doubles as the blob's identity, so a test can tell
// which source's public parameters won.
func (c *ppFallbackContext) setSource(t *testing.T, source, tag string) []byte {
	t.Helper()

	var ppRaw []byte
	switch tag {
	case "":
	case "unknown":
		ppRaw = c.unknownDriverPubParams(t)
	default:
		ppRaw = c.pubParams(t, tag)
	}

	switch source {
	case "storage":
		c.storage.PublicParamsReturns(ppRaw, nil)
	case "configuration":
		if ppRaw != nil {
			c.setConfigPubParams(t, ppRaw)
		}
	case "fetcher":
		c.fetcher.FetchReturns(ppRaw, nil)
	default:
		t.Fatalf("unknown source [%s]", source)
	}

	return ppRaw
}

// TestTMSProviderPublicParamsPriority walks the whole priority matrix: for every combination of
// usable and unusable sources, the public parameters the token service is instantiated with must
// be those of the highest-priority *usable* source.
func TestTMSProviderPublicParamsPriority(t *testing.T) {
	// "" = source holds nothing, "unknown"/"malformed…" = unusable, "ok…" = usable
	for _, tc := range []struct {
		name                              string
		options, storage, config, fetcher string
		winner                            string
	}{
		{name: "options win", options: "ok-options", storage: "ok-storage", config: "ok-config", fetcher: "ok-fetcher", winner: "ok-options"},
		{name: "storage wins when options are unusable", options: "unknown", storage: "ok-storage", config: "ok-config", fetcher: "ok-fetcher", winner: "ok-storage"},
		{name: "storage wins when options are absent", storage: "ok-storage", config: "ok-config", fetcher: "ok-fetcher", winner: "ok-storage"},
		{name: "configuration wins when options and storage are unusable", options: "unknown", storage: "malformed-storage", config: "ok-config", fetcher: "ok-fetcher", winner: "ok-config"},
		{name: "fetcher wins when every local source is unusable", options: "unknown", storage: "malformed-storage", config: "unknown", fetcher: "ok-fetcher", winner: "ok-fetcher"},
		{name: "fetcher wins when every local source is empty", fetcher: "ok-fetcher", winner: "ok-fetcher"},
		{name: "last source standing is used", options: "malformed-options", storage: "", config: "", fetcher: "ok-fetcher", winner: "ok-fetcher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newPPFallbackContext(t)
			var optionsPP []byte
			if tc.options != "" {
				if tc.options == "unknown" {
					optionsPP = c.unknownDriverPubParams(t)
				} else {
					optionsPP = c.pubParams(t, tc.options)
				}
			}
			c.setSource(t, "storage", tc.storage)
			c.setSource(t, "configuration", tc.config)
			c.setSource(t, "fetcher", tc.fetcher)

			tms, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, optionsPP))
			require.NoError(t, err)
			assert.Equal(t, c.tms, tms)

			// the token service must be built with the winning source's public parameters, and
			// the walk must stop there: no later source is instantiated
			require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
			_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
			assert.Equal(t, c.pubParams(t, tc.winner), usedPP)

			// the network is only queried when no local source could serve the TMS
			if tc.winner == "ok-fetcher" {
				assert.Equal(t, 1, c.fetcher.FetchCallCount())
			} else {
				assert.Equal(t, 0, c.fetcher.FetchCallCount(), "the fetcher must not be queried when a local source wins")
			}
		})
	}
}

// TestTMSProviderPublicParamsSourceErrors verifies that each source explains itself in the
// aggregated error, so that logs identify the source that came up empty or unusable.
func TestTMSProviderPublicParamsSourceErrors(t *testing.T) {
	// Test case: a source that returns empty bytes without an error is reported as empty, and
	// does not masquerade as a usable source.
	t.Run("empty sources are reported", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.storage.PublicParamsReturns([]byte{}, nil)
		c.fetcher.FetchReturns([]byte{}, nil)

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.Error(t, err)
		require.ErrorIs(t, err, core.ErrTMSNotFound)
		assert.Contains(t, err.Error(), "no public params found in publicParametersStorage")
		assert.Contains(t, err.Error(), "no public params fetched")
		assert.Equal(t, 0, c.driver.NewTokenServiceCallCount())
	})

	// Test case: input validation is reported without touching any source, and is not an
	// ErrTMSNotFound.
	t.Run("input validation", func(t *testing.T) {
		c := newPPFallbackContext(t)

		_, err := c.provider.NewTokenManagerService(driver.ServiceOptions{Namespace: "ns1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "network not specified")
		require.NotErrorIs(t, err, core.ErrTMSNotFound)

		_, err = c.provider.NewTokenManagerService(driver.ServiceOptions{Network: "n1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace not specified")
		require.NotErrorIs(t, err, core.ErrTMSNotFound)

		assert.Equal(t, 0, c.storage.PublicParamsCallCount())
		assert.Equal(t, 0, c.fetcher.FetchCallCount())
	})

	// Test case: a missing PublicParamsFetcher names the missing collaborator instead of
	// reading like an empty fetch.
	t.Run("missing fetcher is distinguishable from an empty fetch", func(t *testing.T) {
		c := newPPFallbackContext(t)

		_, err := c.provider.NewTokenManagerService(driver.ServiceOptions{Network: "n1", Namespace: "ns1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no PublicParamsFetcher configured")
	})

	// Test case: a nil publicParametersStorage is reported rather than dereferenced, and the
	// walk carries on to the remaining sources.
	t.Run("nil storage is reported and does not stop the walk", func(t *testing.T) {
		c := newPPFallbackContext(t)
		provider := core.NewTMSProvider(c.config, nil, core.NewTokenDriverService([]core.NamedFactory[driver.Driver]{
			{Name: core.TokenDriverIdentifier("test.v1"), Driver: c.driver},
		}))
		fetched := c.pubParams(t, "ok")
		c.fetcher.FetchReturns(fetched, nil)

		tms, err := provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.NoError(t, err)
		assert.Equal(t, c.tms, tms)
		require.Equal(t, 1, c.driver.NewTokenServiceCallCount())
		_, usedPP := c.driver.NewTokenServiceArgsForCall(0)
		assert.Equal(t, fetched, usedPP)
	})

	// Test case: a nil publicParametersStorage with no other source reports itself.
	t.Run("nil storage reports itself", func(t *testing.T) {
		c := newPPFallbackContext(t)
		provider := core.NewTMSProvider(c.config, nil, core.NewTokenDriverService([]core.NamedFactory[driver.Driver]{
			{Name: core.TokenDriverIdentifier("test.v1"), Driver: c.driver},
		}))

		_, err := provider.NewTokenManagerService(driver.ServiceOptions{Network: "n1", Namespace: "ns1"})
		require.Error(t, err)
		require.ErrorIs(t, err, core.ErrTMSNotFound)
		assert.Contains(t, err.Error(), "no publicParametersStorage available")
	})

	// Test case: the configuration source reports its cause once. It used to embed the wrapped
	// error in its own message and then wrap it, rendering the cause twice.
	t.Run("configuration error is rendered once", func(t *testing.T) {
		c := newPPFallbackContext(t)
		c.config.ConfigurationForReturns(nil, errors.New("some-unique-config-failure"))

		_, err := c.provider.NewTokenManagerService(newPPFallbackOpts(c, nil))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load the configuration of")
		assert.Equal(t, 1, strings.Count(err.Error(), "some-unique-config-failure"),
			"the configuration failure must be rendered exactly once")
	})
}

// TestTMSProviderConcurrentGet verifies that concurrent callers racing on the same TMS share a
// single instance: the walk runs once, so the public parameters are deserialized once and the
// network is queried once, no matter how many callers arrive together.
func TestTMSProviderConcurrentGet(t *testing.T) {
	c := newPPFallbackContext(t)
	c.fetcher.FetchReturns(c.pubParams(t, "ok"), nil)

	const callers = 32
	var wg sync.WaitGroup
	results := make([]driver.TokenManagerService, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = c.provider.GetTokenManagerService(newPPFallbackOpts(c, nil))
		}()
	}
	close(start)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, c.tms, results[i])
	}
	assert.Equal(t, 1, c.driver.NewTokenServiceCallCount(), "the TMS must be instantiated once")
	assert.Equal(t, 1, c.fetcher.FetchCallCount(), "the network must be queried once")
}

// TestTMSProviderUpdateRejectsUnusablePublicParams pins the boundary between resolving a TMS and
// updating one: resolution may fall back to another source, an update may not. An update carries
// the public parameters the TMS is to adopt, so when the driver rejects them the update must
// fail. Falling back would report success while the node kept running on an older copy, having
// already unloaded the service it had; and because the options an update is built from carry no
// fetcher, that fallback could only ever land on a stale local copy - the opposite of what the
// walk exists for.
func TestTMSProviderUpdateRejectsUnusablePublicParams(t *testing.T) {
	c := newPPFallbackContext(t)
	// a perfectly usable local copy is available: resolution would fall back to it, an update
	// must not.
	c.storage.PublicParamsReturns(c.pubParams(t, "ok-storage"), nil)

	err := c.provider.Update(newPPFallbackOpts(c, c.unknownDriverPubParams(t)))

	require.Error(t, err)
	require.NotErrorIs(t, err, core.ErrTMSNotFound,
		"the public params were delivered, they were just not usable")
	assert.Contains(t, err.Error(), "[options]", "the error must name the source that failed")
	assert.Equal(t, 0, c.storage.PublicParamsCallCount(), "an update must not read the local storage")
	assert.Equal(t, 0, c.fetcher.FetchCallCount(), "an update must not query the network")
	assert.Equal(t, 0, c.driver.NewTokenServiceCallCount(),
		"no service may be built from public params the driver rejects")
}

// TestTMSProviderUpdateKeepsTheCachedServiceOnFailure verifies that a rejected update is not
// destructive: the service the node is running is neither unloaded nor evicted, so a single bad
// update event cannot take a working TMS down.
func TestTMSProviderUpdateKeepsTheCachedServiceOnFailure(t *testing.T) {
	c := newPPFallbackContext(t)
	running := c.pubParams(t, "ok-running")
	runningDigest := sha256.Sum256(running)
	ppm := &drivermock.PublicParamsManager{}
	ppm.PublicParamsHashReturns(runningDigest[:])
	c.tms.PublicParamsManagerReturns(ppm)

	tms, err := c.provider.GetTokenManagerService(newPPFallbackOpts(c, running))
	require.NoError(t, err)
	require.Equal(t, c.tms, tms)
	require.Equal(t, 1, c.driver.NewTokenServiceCallCount())

	require.Error(t, c.provider.Update(newPPFallbackOpts(c, c.unknownDriverPubParams(t))))

	assert.Equal(t, 0, c.tms.DoneCallCount(), "the running service must not be unloaded")
	again, err := c.provider.GetTokenManagerService(newPPFallbackOpts(c, running))
	require.NoError(t, err)
	assert.Equal(t, tms, again, "the cached service must still be the one served")
	assert.Equal(t, 1, c.driver.NewTokenServiceCallCount(), "the cached service must not be rebuilt")
}

// TestTMSProviderConfigurationFor verifies that the configuration of a TMS can be read without
// instantiating it.
func TestTMSProviderConfigurationFor(t *testing.T) {
	c := newPPFallbackContext(t)
	c.config.ConfigurationForReturns(c.tmsConfig, nil)

	config, err := c.provider.ConfigurationFor("n1", "c1", "ns1")
	require.NoError(t, err)
	assert.Equal(t, c.tmsConfig, config)
	assert.Equal(t, 0, c.driver.NewTokenServiceCallCount(), "reading the configuration must not instantiate the TMS")

	c.config.ConfigurationForReturns(nil, errors.New("no configuration"))
	_, err = c.provider.ConfigurationFor("n1", "c1", "ns1")
	require.Error(t, err)
}
