/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	sdriver "github.com/LFDT-Panurus/panurus/token/services/selector/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
)

// The selector service constructors take variadic Options (to let an application supply its
// own selection rate limiter). dig treats a constructor's variadic parameter as optional and
// calls it with no variadic arguments, which is what makes them usable as providers in
// Install. This test pins that: adding a non-variadic parameter without providing its type,
// or turning the options into a required argument, breaks SDK wiring at runtime only.
func TestSelectorProvidersAreDigCompatible(t *testing.T) {
	for name, provider := range map[string]any{
		"simple":    selectorProviders[sdriver.Simple],
		"sherdlock": selectorProviders[sdriver.Sherdlock],
		"default":   selectorProviders[""],
	} {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, provider)

			c := dig.New()
			// The dependencies both constructors resolve from the container.
			require.NoError(t, c.Provide(func() simple.LockerProvider { return nil }))
			require.NoError(t, c.Provide(func() simple.ConfigProvider { return &stubConfigProvider{} }))
			require.NoError(t, c.Provide(func() sherdlock.ConfigProvider { return &stubConfigProvider{} }))
			require.NoError(t, c.Provide(func() sherdlock.FetcherProvider { return nil }))
			require.NoError(t, c.Provide(func() tokenlockdb.StoreServiceManager { return nil }))
			require.NoError(t, c.Provide(func() metrics.Provider { return &disabled.Provider{} }))

			require.NoError(t, c.Provide(provider, dig.As(new(token.SelectorManagerProvider))))

			var got token.SelectorManagerProvider
			require.NoError(t, c.Invoke(func(p token.SelectorManagerProvider) { got = p }))
			assert.NotNil(t, got)
		})
	}
}

// stubConfigProvider unmarshals nothing, so the selector config falls back to its defaults.
type stubConfigProvider struct{}

func (*stubConfigProvider) UnmarshalKey(string, any) error { return nil }
