/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

// Configuration is the subset of the selector configuration that describes the built-in rate
// limiter. It is implemented by token/services/selector/config.Config.
type Configuration interface {
	// IsRateLimitEnabled tells whether the built-in per-wallet selection rate limiter is on.
	IsRateLimitEnabled() bool
	// GetRateLimit returns the allowed selection requests per second per wallet.
	GetRateLimit() float64
	// GetRateLimitBurst returns the burst capacity of a wallet bucket.
	GetRateLimitBurst() int
	// GetRateLimitMaxBuckets returns the maximum number of live wallet buckets.
	GetRateLimitMaxBuckets() int
}

// Option customizes how a selector service obtains its rate limiter. Options take precedence
// over configuration.
type Option func(*Options)

// Options collects the effect of the Option values passed to a selector service.
type Options struct {
	limiter Limiter
	// set records that an Option explicitly decided the limiter, including the decision to
	// have none (WithLimiter(nil)). Without it, an explicit "off" would be
	// indistinguishable from "not specified" and configuration would win.
	set bool
}

// WithLimiter makes the selector service use the passed limiter, ignoring the
// token.selector.rateLimit* configuration. Pass nil to disable rate limiting outright, whatever
// the configuration says.
//
// The service does not take ownership of the limiter: it never stops it, not even from Shutdown.
// A limiter passed here may therefore be shared between services, and its lifecycle stays with
// the caller.
func WithLimiter(limiter Limiter) Option {
	return func(o *Options) {
		o.limiter = limiter
		o.set = true
	}
}

// WithDefaultLimiter enables the built-in per-wallet limiter with DefaultConfig, whatever the
// token.selector.rateLimit* configuration says.
func WithDefaultLimiter() Option {
	return WithLimiter(New(DefaultConfig()))
}

// CompileOptions applies the passed options.
func CompileOptions(opts ...Option) *Options {
	o := &Options{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}

	return o
}

// Limiter returns the limiter a selector service must use given its options and configuration,
// or nil when selection must not be rate limited. Options win over configuration; without any
// option, the limiter comes from the configuration, which has rate limiting off by default.
//
// A nil Configuration is treated as "not configured", so that a service whose configuration
// failed to parse still starts, with rate limiting off.
func (o *Options) Limiter(c Configuration) Limiter {
	if o.set {
		return o.limiter
	}
	if c == nil || !c.IsRateLimitEnabled() {
		return nil
	}

	return New(Config{
		Rate:       c.GetRateLimit(),
		Burst:      c.GetRateLimitBurst(),
		MaxBuckets: c.GetRateLimitMaxBuckets(),
	})
}
