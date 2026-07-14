/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package driver defines common interfaces and type aliases used by token driver implementations.
// These abstractions decouple drivers from concrete service implementations, facilitating
// modularity, testing, and dependency injection.
package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"go.opentelemetry.io/otel/trace"
)

//go:generate counterfeiter -o mock/metrics_provider.go -fake-name MetricsProvider github.com/LFDT-Panurus/panurus/token/core/common/driver.MetricsProvider
//go:generate counterfeiter -o mock/config_service.go -fake-name ConfigService github.com/LFDT-Panurus/panurus/token/core/common/driver.ConfigService
//go:generate counterfeiter -o mock/identity_provider.go -fake-name IdentityProvider github.com/LFDT-Panurus/panurus/token/core/common/driver.IdentityProvider
//go:generate counterfeiter -o mock/network_provider.go -fake-name NetworkProvider github.com/LFDT-Panurus/panurus/token/core/common/driver.NetworkProvider
//go:generate counterfeiter -o mock/vault_provider.go -fake-name VaultProvider github.com/LFDT-Panurus/panurus/token/core/common/driver.VaultProvider
//go:generate counterfeiter -o mock/local_membership.go -fake-name LocalMembership github.com/LFDT-Panurus/panurus/token/services/network/driver.LocalMembership
//go:generate counterfeiter -o mock/network_driver.go -fake-name Network github.com/LFDT-Panurus/panurus/token/services/network/driver.Network
//go:generate counterfeiter -o mock/keystore.go -fake-name Keystore github.com/LFDT-Panurus/panurus/token/services/identity/driver.Keystore
//go:generate counterfeiter -o mock/ici.go -fake-name IdentityConfigurationIterator github.com/LFDT-Panurus/panurus/token/services/identity/driver.IdentityConfigurationIterator

// MetricsProvider is an alias for the metrics.Provider interface.
type MetricsProvider = metrics.Provider

// TracerProvider is an alias for the trace.TracerProvider interface from OpenTelemetry.
type TracerProvider = trace.TracerProvider

// StorageProvider is an alias for the identity.StorageProvider interface.
type StorageProvider = identity.StorageProvider

// NetworkBinderService is an alias for the identity.NetworkBinderService interface.
type NetworkBinderService = identity.NetworkBinderService

// ConfigService defines the interface for accessing TMS configurations.
type ConfigService interface {
	// ConfigurationFor returns the configuration for the given network, channel, and namespace.
	ConfigurationFor(network, channel, namespace string) (driver.Configuration, error)
}

// IdentityProvider defines the interface for accessing identities.
type IdentityProvider interface {
	// DefaultIdentity returns the default identity.
	DefaultIdentity() driver.Identity
}

// NetworkProvider defines the interface for accessing network instances.
type NetworkProvider interface {
	// GetNetwork returns the network instance for the given network and channel.
	GetNetwork(network, channel string) (*network.Network, error)
}

// VaultProvider defines the interface for accessing vault instances.
type VaultProvider interface {
	// Vault returns the vault instance for the given network, channel, and namespace.
	Vault(network, channel, namespace string) (driver.Vault, error)
}
