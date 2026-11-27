/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package skis

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// LegacyPolicyOwnerType is the owner_type that Tokens rows carry for policy owners
// stored before identity.TypeToString returned "policy" for them.
const LegacyPolicyOwnerType = "Type (6)"

// NewSKIExtractor returns the SKI extractor shared by the skicleanup commands.
// It registers idemix, idemixnym (backed by identityStore) and a no-op for x509, as the
// runtime cleanup service does. Other types, composites included, use the SHA-256 fallback;
// the runtime registers a no-op for composites instead.
func NewSKIExtractor(identityStore idemixnym.IdentityStoreService) *cleanup.SKIExtractor {
	extractor := cleanup.NewSKIExtractor()
	extractor.RegisterProvider(idemix.IdentityTypeString, idemix.NewSKIProvider())
	extractor.RegisterProvider(idemixnym.IdentityTypeString, idemixnym.NewSKIProvider(identityStore))
	// X.509 deliberately derives no SKIs: an X.509 key belongs to the wallet,
	// not to a single token. See cleanup.NoopSKIProvider.
	extractor.RegisterProvider(x509.IdentityTypeString, cleanup.NewNoopSKIProvider())

	return extractor
}

// signerInfoStore is the identity store view the owned-skis extractor needs.
type signerInfoStore interface {
	idemixnym.IdentityStoreService
	SignerInfoExists(ctx context.Context, id []byte) (bool, error)
}

// NewOwnedSKIExtractor returns the owned-skis extractor: NewSKIExtractor plus
// providers that unwrap htlc, multisig and policy owners into their components.
// Composites are unwrapped here only: a keep-list is safe with more SKIs, a delete path is not.
// A top-level idemixnym owner stays strict. Components go through an inner extractor that
// skips idemixnym components with no signer info row: they belong to a counterparty.
func NewOwnedSKIExtractor(store signerInfoStore) *cleanup.SKIExtractor {
	inner := NewSKIExtractor(store)
	inner.RegisterProvider(idemixnym.IdentityTypeString, &componentNymSKIProvider{store: store, delegate: idemixnym.NewSKIProvider(store)})
	registerComposites(inner, inner)

	top := NewSKIExtractor(store)
	registerComposites(top, inner)

	return top
}

// registerComposites registers the composite providers on extractor; they recurse into components.
func registerComposites(extractor *cleanup.SKIExtractor, components SKIExtractor) {
	extractor.RegisterProvider(identity.TypeToString(htlc.ScriptType), &compositeSKIProvider{extractor: components, components: htlcComponents})
	extractor.RegisterProvider(identity.TypeToString(multisig.Multisig), &compositeSKIProvider{extractor: components, components: multisigComponents})
	policy := &compositeSKIProvider{extractor: components, components: policyComponents}
	extractor.RegisterProvider(identity.TypeToString(boolpolicy.Policy), policy)
	// Rows stored before identity.TypeToString gained the policy case carry this type.
	extractor.RegisterProvider(LegacyPolicyOwnerType, policy)
}

// componentNymSKIProvider extracts the SKIs of an idemixnym composite component.
// No signer info row means the component is not ours: no SKIs. Otherwise it delegates,
// so a row with empty info (a presence marker) or bad info is an error.
type componentNymSKIProvider struct {
	store    signerInfoStore
	delegate cleanup.TypedSKIProvider
}

// GetSKIsFromIdentity implements cleanup.TypedSKIProvider.
func (p *componentNymSKIProvider) GetSKIsFromIdentity(ctx context.Context, id []byte) ([]string, error) {
	exists, err := p.store.SignerInfoExists(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "check signer info")
	}
	if !exists {
		return nil, nil
	}

	return p.delegate.GetSKIsFromIdentity(ctx, id)
}

// compositeSKIProvider derives the SKIs of the component identities of a composite owner.
// raw is the TypedIdentity payload, as stored in the Tokens table owner_identity column.
// Each component is a TypedIdentity and goes back through extractor with its own type.
// Depth and fan-out are bounded by the token/driver composite-identity limits carried by ctx.
type compositeSKIProvider struct {
	extractor  SKIExtractor
	components func(raw []byte) ([]token.Identity, error)
}

// GetSKIsFromIdentity implements cleanup.TypedSKIProvider.
func (p *compositeSKIProvider) GetSKIsFromIdentity(ctx context.Context, raw []byte) ([]string, error) {
	ctx, err := tdriver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := p.components(raw)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("composite identity has no components")
	}
	if maxComponents := tdriver.MaxIdentityComponentsFrom(ctx); len(ids) > maxComponents {
		return nil, errors.Wrapf(tdriver.ErrTooManyIdentityComponents, "got %d, the maximum is %d", len(ids), maxComponents)
	}

	var skis []string
	for k, id := range ids {
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil {
			return nil, errors.Wrapf(err, "component %d", k)
		}
		if len(ti.Identity) == 0 {
			return nil, errors.Errorf("component %d has an empty identity", k)
		}
		s, err := p.extractor.GetSKIsFromIdentity(ctx, ti.Identity, identity.TypeToString(ti.Type))
		if err != nil {
			return nil, errors.Wrapf(err, "component %d", k)
		}
		skis = append(skis, s...)
	}

	return skis, nil
}

// htlcComponents returns the sender and the recipient of an htlc script.
// They are the only identities a script holds.
func htlcComponents(raw []byte) ([]token.Identity, error) {
	script := &htlc.Script{}
	if err := json.Unmarshal(raw, script); err != nil {
		return nil, errors.Wrap(err, "unmarshal htlc script")
	}

	return []token.Identity{script.Sender, script.Recipient}, nil
}

func multisigComponents(raw []byte) ([]token.Identity, error) {
	mi := &multisig.MultiIdentity{}
	if err := mi.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "deserialize multisig identity")
	}

	return mi.Identities, nil
}

func policyComponents(raw []byte) ([]token.Identity, error) {
	pi := &boolpolicy.PolicyIdentity{}
	if err := pi.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "deserialize policy identity")
	}
	ids := make([]token.Identity, len(pi.Identities))
	for k, id := range pi.Identities {
		ids[k] = id
	}

	return ids, nil
}
