/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	fsc "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func WithIssuerIdentity(label string, hsm bool) fsc.Option {
	if hsm {
		return WithIssuerIdentityWithHSM(label)
	}

	return func(o *fsc.Options) error {
		to := topology.ToOptions(o)
		to.SetIssuers(append(to.Issuers(), label))

		if label != "_default_" {
			o.AddAlias(label)
		}

		return nil
	}
}

func WithIssuerIdentityWithHSM(label string) fsc.Option {
	return func(o *fsc.Options) error {
		to := topology.ToOptions(o)
		to.SetIssuers(append(to.Issuers(), label))
		to.UseHSMForIssuer(label)

		if label != "_default_" {
			o.AddAlias(label)
		}

		return nil
	}
}

func WithDefaultIssuerIdentity(hsm bool) fsc.Option {
	if hsm {
		return WithDefaultIssuerIdentityWithHSM()
	}

	return WithIssuerIdentity("_default_", false)
}

func WithDefaultIssuerIdentityWithHSM() fsc.Option {
	return WithIssuerIdentityWithHSM("_default_")
}

func WithDefaultOwnerIdentity() fsc.Option {
	return WithOwnerIdentity("_default_")
}

func WithOwnerIdentity(label string) fsc.Option {
	return func(o *fsc.Options) error {
		to := topology.ToOptions(o)
		to.SetOwners(append(to.Owners(), label))

		if label != "_default_" {
			o.AddAlias(label)
		}

		return nil
	}
}

// WithRemoteOwnerIdentity adds a new owner identity and mark it as remote.
func WithRemoteOwnerIdentity(label string) fsc.Option {
	return func(o *fsc.Options) error {
		to := topology.ToOptions(o)
		to.SetOwners(append(to.Owners(), label))
		to.SetRemoteOwner(label)

		if label != "_default_" {
			o.AddAlias(label)
		}

		return nil
	}
}

func WithCertifierIdentity() fsc.Option {
	return func(o *fsc.Options) error {
		topology.ToOptions(o).SetCertifier(true)

		return nil
	}
}

func WithAuditorIdentity(hsm bool) fsc.Option {
	if hsm {
		return WithAuditorIdentityWithHSM()
	}

	return func(o *fsc.Options) error {
		topology.ToOptions(o).SetAuditor(true)

		return nil
	}
}

func WithAuditorIdentityWithHSM() fsc.Option {
	return func(o *fsc.Options) error {
		topology.ToOptions(o).SetAuditor(true)
		topology.ToOptions(o).UseHSMForAuditor()

		return nil
	}
}

// WithDefaultCacheSize sets the owner wallets default cache size of the node.
// It defaults to topology.DefaultOwnerCacheSize. A negative size is an error.
func WithDefaultCacheSize(n int) fsc.Option {
	return func(o *fsc.Options) error {
		if n < 0 {
			return errors.Errorf("invalid default cache size [%d]: must be non-negative", n)
		}
		topology.ToOptions(o).SetDefaultCacheSize(n)

		return nil
	}
}

// WithStorageCleanupDisabled disables the token storage cleanup of the node. It is enabled by default.
func WithStorageCleanupDisabled() fsc.Option {
	return func(o *fsc.Options) error {
		topology.ToOptions(o).SetStorageCleanupDisabled(true)

		return nil
	}
}
