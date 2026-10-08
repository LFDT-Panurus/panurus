/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

// enrollmentKeyPrefix namespaces a resolved enrollment-ID throttle key so it can never collide
// with the fallback keyspace, which is a raw Identity.UniqueID(). UniqueID() is a content hash and
// an enrollment ID is an operator-assigned string, so a collision is already implausible; the
// prefix keeps the two keyspaces provably disjoint regardless.
const enrollmentKeyPrefix = "eid:"

// enrollmentResolver is the slice of driver.IdentityProvider that the enrollment-ID resolver
// needs: a local audit-info lookup and the enrollment-ID extraction. It is declared on the
// consumer side (rather than taking the whole provider) so the resolver can be exercised with a
// small fake. driver.IdentityProvider satisfies it.
type enrollmentResolver interface {
	// GetAuditInfo returns the locally stored audit information for identity, or an error / empty
	// slice when none is known.
	GetAuditInfo(ctx context.Context, identity driver.Identity) ([]byte, error)
	// GetEIDAndRH extracts the enrollment ID and revocation handle carried by auditInfo.
	GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error)
}

// enrollmentKeyResolver is a PrincipalKeyResolver that folds a party's rotating pseudonyms onto its
// enrollment ID, so that a throttle quota follows the party rather than the pseudonym it presents.
//
// It resolves only from locally known audit information: it asks the identity provider for the
// identity's audit info and, when that is present, extracts the enrollment ID from it. An identity
// with no locally known audit info (the typical unregistered rotating pseudonym) resolves to "",
// which tells the gate to fall back to the identity hash — the safe default. It therefore does no
// network or remote resolution on the signature path, as PrincipalKeyResolver requires.
//
// One accepted cost: driver.IdentityProvider.GetAuditInfo is itself instrumented, so consulting it
// here emits one extra OpGetAuditInfo observation per gated operation. That is benign — it only
// inflates a principal's window sample total (OpEscalation events are ignored by the policy's own
// Observe, so there is no feedback loop) — and it is paid only when a gate is actually installed.
type enrollmentKeyResolver struct {
	provider enrollmentResolver
}

// newEnrollmentKeyResolver returns a resolver backed by provider. provider is the identity
// provider the signature service already holds (token manager service's IdentityProvider()).
func newEnrollmentKeyResolver(provider enrollmentResolver) *enrollmentKeyResolver {
	return &enrollmentKeyResolver{provider: provider}
}

// ThrottleKey returns the enrollment-ID throttle key for id, or "" to fall back to id.UniqueID().
// It returns "" whenever the identity cannot be resolved from local audit info — an unregistered
// pseudonym, an empty identity, or any lookup error — rather than chasing it on the hot path.
func (r *enrollmentKeyResolver) ThrottleKey(ctx context.Context, id Identity) string {
	if r == nil || r.provider == nil {
		return ""
	}

	auditInfo, err := r.provider.GetAuditInfo(ctx, id)
	if err != nil || len(auditInfo) == 0 {
		return ""
	}

	eid, _, err := r.provider.GetEIDAndRH(ctx, id, auditInfo)
	if err != nil || eid == "" {
		return ""
	}

	return enrollmentKeyPrefix + eid
}
