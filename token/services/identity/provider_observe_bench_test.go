/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/throttle"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BenchmarkGetSignerAndSign measures the hot path with instrumentation off and with the full
// stack (metrics, audit log, throttle policy) installed. Signing happens once per transaction,
// so the difference between the two is the price of the feature and belongs in review.
func BenchmarkGetSignerAndSign(b *testing.B) {
	id := driver.Identity("an_identity")
	message := []byte("message")

	newProvider := func(b *testing.B) *identity.Provider {
		b.Helper()

		signer := &drvmock.Signer{}
		signer.SignReturns([]byte("sigma"), nil)
		des := &idmock.Deserializer{}
		des.DeserializeSignerReturns(signer, nil)

		return identity.NewProvider(logging.MustGetLogger(), &idmock.Storage{}, des,
			&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, identity.NewMetrics(nil))
	}

	run := func(b *testing.B, p *identity.Provider) {
		b.Helper()
		ctx := b.Context()

		// Warm the signer cache: the steady state of a signing node is a cache hit.
		_, err := p.GetSigner(ctx, id)
		require.NoError(b, err)

		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			signer, err := p.GetSigner(ctx, id)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := signer.Sign(message); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.Run("observer off", func(b *testing.B) {
		run(b, newProvider(b))
	})

	b.Run("observer on", func(b *testing.B) {
		p := newProvider(b)
		stack, err := sigpolicy.New(logging.MustGetLogger(), nil, identity.NewMetrics(nil))
		require.NoError(b, err)
		b.Cleanup(stack.Stop)
		p.SetObserver(stack.Observer())

		run(b, p)
	})
}

// TestGetSignerAndSignDisabledAllocatesNothing pins the zero-cost claim behind "observer off"
// above: a throttle policy configured off, with neither metrics nor a logger, must collapse to
// sigobserve.Nop so that signing and verifying are unwrapped and the hot path allocates nothing
// extra. A regression here would mean "off" no longer means "feature absent".
func TestGetSignerAndSignDisabledAllocatesNothing(t *testing.T) {
	stack, err := sigpolicy.New(nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(stack.Stop)

	assert.Equal(t, throttle.ModeMonitor, stack.Config().Mode, "the default mode is monitor, not off; disable it explicitly below")

	offStack, err := sigpolicy.New(nil, &fixedModeConfigService{mode: throttle.ModeOff}, nil)
	require.NoError(t, err)
	t.Cleanup(offStack.Stop)

	assert.Equal(t, sigobserve.Nop, offStack.Observer(), "mode off with no sinks must collapse to Nop")

	sigma := []byte("sigma")
	signer := plainSigner{sigma: sigma}
	wrapped := sigobserve.InstrumentSigner(signer, offStack.Observer(), "principal", sigobserve.RoleUnknown)
	message := []byte("message")

	allocs := testing.AllocsPerRun(100, func() {
		if _, err := wrapped.Sign(message); err != nil {
			t.Fatal(err)
		}
	})
	assert.InDelta(t, 0.0, allocs, 0, "signing through a disabled stack's observer must not allocate")
}

// plainSigner is a driver.Signer with no recording overhead of its own, so that a benchmark or
// allocation assertion measures only the cost the wrapper adds.
type plainSigner struct {
	sigma []byte
}

func (s plainSigner) Sign([]byte) ([]byte, error) { return s.sigma, nil }

// fixedModeConfigService serves a throttle configuration with only Mode set, letting the rest
// default.
type fixedModeConfigService struct {
	mode throttle.Mode
}

func (c *fixedModeConfigService) UnmarshalKey(_ string, rawVal any) error {
	cfg, ok := rawVal.(*throttle.Config)
	if !ok {
		return nil
	}
	cfg.Mode = c.mode

	return nil
}
