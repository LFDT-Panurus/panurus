/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"context"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
)

// ConfIDResolver resolves the identity configuration id (conf_id, see
// driver.IdentityConfiguration.UniqueID) that an identity was bound under, regardless of role.
// It mirrors the read side of idriver.WalletStoreService.GetConfID.
//
//go:generate counterfeiter -o mock/confid_resolver.go -fake-name ConfIDResolver . ConfIDResolver
type ConfIDResolver interface {
	// GetConfID returns the conf_id the identity was bound under, or an empty string and no
	// error if the identity has no known binding.
	GetConfID(ctx context.Context, identity driver.Identity) (string, error)
}

// SignerRouter routes signer reconstruction directly to the single KeyManager pinned by an
// identity's conf_id, bypassing the fallback deserializer's linear scan across every KeyManager
// registered under the identity's type and the cryptographic probe that scan relies on to detect
// a mismatched KeyManager. Skipping that probe is only safe once the KeyManager is pinned this
// way, so Resolve reports ok=false (never an error) whenever routing cannot be attempted -
// callers MUST fall back to the probing deserializer in that case, not treat it as a hard failure.
type SignerRouter struct {
	mu             sync.RWMutex
	byConfID       map[string]idriver.SignerDeserializer
	confIDResolver ConfIDResolver
	metrics        *Metrics
}

// NewSignerRouter returns an empty SignerRouter. It resolves nothing until KeyManagers are
// registered via Register and a resolver is set via SetConfIDResolver. m may be nil, in which
// case the router's metrics are a noop.
func NewSignerRouter(m *Metrics) *SignerRouter {
	if m == nil {
		m = NewMetrics(nil)
	}

	return &SignerRouter{byConfID: map[string]idriver.SignerDeserializer{}, metrics: m}
}

// SetConfIDResolver sets the resolver used to map an identity to its conf_id. Callers typically
// set this once the wallet store backing the resolver becomes available, which can be after the
// router has already had KeyManagers registered with it.
func (r *SignerRouter) SetConfIDResolver(resolver ConfIDResolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.confIDResolver = resolver
}

// Register binds confID to the deserializer (KeyManager) that originated it. A later
// registration for the same confID replaces the earlier one.
func (r *SignerRouter) Register(confID string, deserializer idriver.SignerDeserializer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byConfID[confID] = deserializer
	r.metrics.SignerRouterRegistrations.Add(1)
}

// Unregister drops the entries for the given conf_ids. Callers releasing the KeyManagers behind
// those conf_ids (LocalMembership.Close) must unregister them: a released KeyManager left in the
// map stays routable, and Resolve would dispatch to it with the cryptographic probe skipped.
// conf_ids that were never registered are ignored.
func (r *SignerRouter) Unregister(confIDs ...string) {
	if len(confIDs) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, confID := range confIDs {
		delete(r.byConfID, confID)
	}
}

// Close drops every registration, so the router pins no KeyManager and Resolve reports ok=false
// for every identity until KeyManagers are registered again. It is the teardown counterpart of
// Register for a whole router, where Unregister covers a single membership's share of it, and is
// idempotent. Close does not close the KeyManagers themselves: the router borrows them, their
// owner (LocalMembership) releases them.
func (r *SignerRouter) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byConfID = map[string]idriver.SignerDeserializer{}
}

// Len returns the number of conf_id->KeyManager bindings currently held.
func (r *SignerRouter) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.byConfID)
}

// Resolve attempts to reconstruct a Signer for id via conf_id-based routing. ok is false, with a
// nil error, whenever routing cannot be attempted (no resolver set, no conf_id mapping, no
// KeyManager registered for that conf_id) or the routed KeyManager itself fails to reconstruct
// the signer - callers MUST treat ok=false as "try the fallback path", never as a hard failure.
func (r *SignerRouter) Resolve(ctx context.Context, id driver.Identity) (driver.Signer, bool) {
	r.mu.RLock()
	resolver := r.confIDResolver
	m := r.metrics
	r.mu.RUnlock()
	if resolver == nil {
		return nil, false
	}

	confID, err := resolver.GetConfID(ctx, id)
	if err != nil || confID == "" {
		return nil, false
	}

	r.mu.RLock()
	deserializer, ok := r.byConfID[confID]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}

	// The fallback multiplex dispatches on the unwrapped identity bytes (see
	// TypedSignerDeserializerMultiplex.DeserializeSigner); mirror that here so the routed
	// KeyManager receives the same bytes it would on the fallback path.
	typed, err := UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, false
	}

	if probeFree, ok := deserializer.(idriver.ProbeFreeSignerDeserializer); ok {
		signer, err := probeFree.DeserializeSignerNoProbe(ctx, typed.Identity)
		if err != nil {
			m.NoProbeErrors.Add(1)

			return nil, false
		}

		return signer, true
	}

	signer, err := deserializer.DeserializeSigner(ctx, typed.Identity)
	if err != nil {
		return nil, false
	}

	return signer, true
}
