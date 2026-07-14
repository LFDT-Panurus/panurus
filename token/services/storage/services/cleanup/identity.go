/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// TypedSKIProvider defines the interface for identity-type-specific SKI extraction.
// Implementations should extract SKIs (Subject Key Identifiers) from identity bytes
// according to the specific identity format (e.g., Idemix, X.509).
//
// The provider is responsible for:
//   - Parsing the identity bytes according to its format
//   - Extracting the relevant key material
//   - Computing one or more SKIs from that key material
//   - Returning the SKIs in hexadecimal string format
//
// Example implementations:
//   - IdemixSKIProvider: Extracts SKI from NymPublicKey in Idemix identity
//   - X509SKIProvider: Extracts SKI from certificate's public key
//   - FallbackSKIProvider: Computes SHA256 hash of entire identity bytes
type TypedSKIProvider interface {
	// GetSKIsFromIdentity derives one or more SKIs from an identity's raw bytes.
	// Returns:
	//   - []string: List of SKI values in hexadecimal format
	//   - error: Any error encountered during extraction
	//
	// If the identity is invalid or empty, implementations should return (nil, nil)
	// rather than an error, to allow graceful handling.
	GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error)
}

// FallbackSKIProvider implements TypedSKIProvider using a simple SHA256 hash approach.
// This provider computes the SHA256 hash of the entire identity bytes as the SKI.
// It serves as a default/fallback for identity types without specific providers.
type FallbackSKIProvider struct{}

// NewFallbackSKIProvider creates a new fallback SKI provider
func NewFallbackSKIProvider() *FallbackSKIProvider {
	return &FallbackSKIProvider{}
}

// GetSKIsFromIdentity computes the SHA256 hash of the identity bytes as the SKI.
// This is a simplified approach that works as a reasonable default for most identity types.
// Returns an empty slice if the identity is empty.
func (p *FallbackSKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	if len(identity) == 0 {
		logger.Warn("Empty identity, cannot derive SKI")

		return nil, nil
	}

	// Compute SHA256 hash of the identity as the SKI
	ski := sha256.Sum256(identity)
	skiHex := hex.EncodeToString(ski[:])

	logger.Debugf("Derived SKI using fallback provider: %s", skiHex)

	return []string{skiHex}, nil
}

// SKIExtractor orchestrates SKI extraction by delegating to identity-type-specific providers.
// It maintains a registry of TypedSKIProvider implementations, one per identity type,
// and routes extraction requests to the appropriate provider based on the identity type.
//
// Architecture:
//   - providers: Map from identity type string (e.g., "idemix", "x509") to TypedSKIProvider
//   - defaultProvider: Fallback provider used when no type-specific provider is registered
//
// Thread-safety: Provider registration should occur during initialization only.
// The GetSKIsFromIdentity method is safe for concurrent use after initialization.
type SKIExtractor struct {
	mu              sync.RWMutex
	providers       map[string]TypedSKIProvider
	defaultProvider TypedSKIProvider
}

// NewSKIExtractor creates a new SKI extractor with a fallback provider as default.
// The extractor is initialized with an empty provider registry and uses
// FallbackSKIProvider for all identity types until specific providers are registered.
func NewSKIExtractor() *SKIExtractor {
	return &SKIExtractor{
		providers:       make(map[string]TypedSKIProvider),
		defaultProvider: NewFallbackSKIProvider(),
	}
}

// RegisterProvider registers a TypedSKIProvider for a specific identity type.
// This allows identity-type-specific SKI extraction logic to be plugged in.
//
// Parameters:
//   - identityType: The identity type identifier (e.g., "idemix", "x509", "fabtoken")
//   - provider: The TypedSKIProvider implementation for this identity type
//
// Example:
//
//	extractor.RegisterProvider("idemix", NewIdemixSKIProvider())
//	extractor.RegisterProvider("x509", NewX509SKIProvider())
//
// Note: This method should be called during initialization, before concurrent access.
func (s *SKIExtractor) RegisterProvider(identityType string, provider TypedSKIProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[identityType] = provider
	logger.Debugf("Registered SKI provider for identity type: %s", identityType)
}

// SetDefaultProvider sets the fallback provider used for unknown identity types.
// If not called, FallbackSKIProvider is used by default.
//
// Parameters:
//   - provider: The TypedSKIProvider to use as default
//
// Note: This method should be called during initialization, before concurrent access.
func (s *SKIExtractor) SetDefaultProvider(provider TypedSKIProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultProvider = provider
	logger.Debugf("Set default SKI provider")
}

// GetSKIsFromIdentity derives one or more SKIs from an owner identity by delegating
// to the appropriate TypedSKIProvider based on the identity type.
//
// The method:
//  1. Looks up a registered provider for the given identityType
//  2. If found, delegates to that provider
//  3. If not found, uses the default provider
//  4. Returns the SKIs in hexadecimal string format
//
// Parameters:
//   - ctx: Context for cancellation and tracing
//   - identity: Raw identity bytes
//   - identityType: The type of identity (e.g., "idemix", "x509")
//
// Returns:
//   - []string: List of SKI values in hexadecimal format
//   - error: Any error encountered during extraction
//
// Returns an empty slice if the identity is empty or invalid.
func (s *SKIExtractor) GetSKIsFromIdentity(ctx context.Context, identity []byte, identityType string) ([]string, error) {
	if len(identity) == 0 {
		logger.Warn("Empty identity, cannot derive SKI")

		return nil, nil
	}

	// Get the appropriate provider
	s.mu.RLock()
	provider, exists := s.providers[identityType]
	if !exists {
		provider = s.defaultProvider
		logger.Debugf("No specific provider for identity type [%s], using default provider", identityType)
	} else {
		logger.Debugf("Using registered provider for identity type [%s]", identityType)
	}
	s.mu.RUnlock()

	// Delegate to the provider
	skis, err := provider.GetSKIsFromIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}

	if len(skis) > 0 {
		logger.Debugf("Derived %d SKI(s) for identity type [%s]", len(skis), identityType)
	}

	return skis, nil
}
