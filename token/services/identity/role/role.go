/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"context"
	"runtime/debug"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// LocalMembership abstracts the local identity/membership service used by a Role.
// The Role relies on this interface to lookup identities, identifiers and to
// register identities. The concrete implementation is provided by the local
// membership/identity provider and a counterfeiter-generated fake exists under
// `mock` for unit tests.
//
//go:generate counterfeiter -o mock/lm.go -fake-name LocalMembership . LocalMembership
type LocalMembership interface {
	DefaultNetworkIdentity() driver.Identity
	IsMe(ctx context.Context, id driver.Identity) bool
	GetIdentityInfo(ctx context.Context, label string, auditInfo []byte) (idriver.IdentityInfo, error)
	GetIdentifier(ctx context.Context, id driver.Identity) (string, error)
	GetDefaultIdentifier() string
	RegisterIdentity(ctx context.Context, config driver.IdentityConfiguration) error
	IDs() ([]string, error)
	Close()
}

// Role models a role whose identities are anonymous.
// A Role delegates identity resolution to the LocalMembership implementation.
// It exposes mapping helpers that accept either a string identifier, a driver.Identity
// or raw []byte and returns either a long-term identity (driver.Identity) and/or
// an identifier string used by the token subsystem.
type Role struct {
	logger          logging.Logger
	roleID          idriver.IdentityRoleType
	networkID       string
	localMembership LocalMembership
	nodeIdentity    driver.Identity
}

func NewRole(
	logger logging.Logger,
	roleID idriver.IdentityRoleType,
	networkID string,
	nodeIdentity driver.Identity,
	localMembership LocalMembership,
) *Role {
	return &Role{
		logger:          logger,
		roleID:          roleID,
		networkID:       networkID,
		localMembership: localMembership,
		nodeIdentity:    nodeIdentity,
	}
}

// ID returns the role identifier (RoleType) for this Role instance.
func (r *Role) ID() idriver.IdentityRoleType {
	return r.roleID
}

// GetIdentityInfo returns the identity information for the given identity identifier.
// It simply forwards the call to the LocalMembership and annotates any returned
// error with the network context for easier diagnosis.
func (r *Role) GetIdentityInfo(ctx context.Context, id string) (idriver.IdentityInfo, error) {
	r.logger.DebugfContext(ctx, "[%s] getting info for [%s]", r.networkID, logging.Printable(id))

	info, err := r.localMembership.GetIdentityInfo(ctx, id, nil)
	if err != nil {
		return nil, errors.WithMessagef(err, "[%s] failed to get identity for [%s]", r.networkID, id)
	}

	return info, nil
}

// RegisterIdentity registers the given identity via the LocalMembership service.
func (r *Role) RegisterIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	return r.localMembership.RegisterIdentity(ctx, config)
}

// IdentityIDs returns the identifiers known to this Role (delegates to LocalMembership.IDs).
func (r *Role) IdentityIDs() ([]string, error) {
	return r.localMembership.IDs()
}

// Done releases all the resources allocated by this service.
func (r *Role) Done() error {
	r.localMembership.Close()

	return nil
}

// MapToIdentity returns the identity for the given WalletLookupID argument.
// The WalletLookupID can be a string (label), []byte or driver.Identity. The
// method dispatches to helper functions that implement the mapping logic.
func (r *Role) MapToIdentity(ctx context.Context, v driver.WalletLookupID) (driver.Identity, string, error) {
	switch vv := v.(type) {
	case driver.Identity:
		return r.mapIdentityToID(ctx, vv)
	case []byte:
		// []byte identities are treated as driver.Identity (cast) by the mapper
		return r.mapIdentityToID(ctx, vv)
	case string:
		return r.mapStringToID(ctx, vv)
	default:
		// For unsupported types return a descriptive error. The stack is appended
		// to help debugging callers that may accidentally pass incorrect types.
		return nil, "", errors.Errorf("identifier not recognised, expected []byte or driver.Identity, got [%T], [%s]", v, string(debug.Stack()))
	}
}

// mapStringToID implements the resolution logic when the caller provides a string
// label. The resolution order is deliberately chosen:
// 1. Ask LocalMembership for an identifier associated to the label
// 2. Handle empty label and default-identity aliases (default identifier, default network identity, node identity)
// 3. If the label corresponds to a local member (IsMe), try to fetch its identifier; if not available return the identity
// 4. Fallback: return the string label as identifier
func (r *Role) mapStringToID(ctx context.Context, v string) (driver.Identity, string, error) {
	defaultNetworkIdentity := r.localMembership.DefaultNetworkIdentity()
	defaultIdentifier := r.localMembership.GetDefaultIdentifier()

	r.logger.DebugfContext(ctx, "[%s] mapping string identifier for [%s,%s], default identities [%s:%s]",
		r.networkID,
		logging.Printable(v),
		utils.Hashable(v),
		defaultNetworkIdentity,
		r.nodeIdentity,
	)

	label := v
	labelAsIdentity := driver.Identity(label)

	// check immediately if there is an identifier with that label
	if idIdentifier, err := r.localMembership.GetIdentifier(ctx, labelAsIdentity); err == nil {
		return nil, idIdentifier, nil
	}

	switch {
	case len(label) == 0:
		r.logger.DebugfContext(ctx, "passed empty label")

		return nil, defaultIdentifier, nil
	case label == defaultIdentifier:
		r.logger.DebugfContext(ctx, "passed default identifier")

		return nil, defaultIdentifier, nil
	case label == defaultNetworkIdentity.UniqueID():
		r.logger.DebugfContext(ctx, "passed default identity")

		return nil, defaultIdentifier, nil
	case label == string(defaultNetworkIdentity):
		r.logger.DebugfContext(ctx, "passed default identity as string")

		return nil, defaultIdentifier, nil
	case defaultNetworkIdentity.Equal(labelAsIdentity):
		r.logger.DebugfContext(ctx, "passed default identity as view identity")

		return nil, defaultIdentifier, nil
	case r.nodeIdentity.Equal(labelAsIdentity):
		r.logger.DebugfContext(ctx, "passed node identity as view identity")

		return nil, defaultIdentifier, nil
	case r.localMembership.IsMe(ctx, labelAsIdentity):
		r.logger.DebugfContext(ctx, "passed a local member")
		id := labelAsIdentity
		if idIdentifier, err := r.localMembership.GetIdentifier(ctx, id); err == nil {
			return nil, idIdentifier, nil
		}
		r.logger.DebugfContext(ctx, "failed getting identity info for [%s], returning the identity", id)

		return id, "", nil
	}

	r.logger.DebugfContext(ctx, "cannot find match for string [%s]", v)

	return nil, label, nil
}

// mapIdentityToID implements the resolution logic when the caller provides a
// driver.Identity (or []byte cast to driver.Identity). It checks for empty
// identities, equality with the default network or node identity, local membership
// (IsMe) and finally tries to resolve the identifier by querying the
// LocalMembership for IdentityInfo or Identifier.
func (r *Role) mapIdentityToID(ctx context.Context, v driver.Identity) (driver.Identity, string, error) {
	defaultNetworkIdentity := r.localMembership.DefaultNetworkIdentity()
	defaultIdentifier := r.localMembership.GetDefaultIdentifier()

	r.logger.DebugfContext(ctx, "[%s] mapping driver.Identity identifier for [%s], default identities [%s:%s]",
		r.networkID,
		v,
		defaultNetworkIdentity,
		r.nodeIdentity,
	)

	id := v
	switch {
	case id.IsNone():
		r.logger.DebugfContext(ctx, "passed empty identity")

		return nil, defaultIdentifier, nil
	case id.Equal(defaultNetworkIdentity):
		r.logger.DebugfContext(ctx, "passed default identity")

		return nil, defaultIdentifier, nil
	case string(id) == defaultIdentifier:
		r.logger.DebugfContext(ctx, "passed default identifier")

		return nil, defaultIdentifier, nil
	case id.Equal(r.nodeIdentity):
		r.logger.DebugfContext(ctx, "passed identity is the node identity (same bytes)")

		return nil, defaultIdentifier, nil
	case r.localMembership.IsMe(ctx, id):
		r.logger.DebugfContext(ctx, "passed identity is me")
		if idIdentifier, err := r.localMembership.GetIdentifier(ctx, id); err == nil {
			return id, idIdentifier, nil
		}
		r.logger.DebugfContext(ctx, "failed getting identity info for [%s], returning the identity", id)

		return id, "", nil
	}
	r.logger.DebugfContext(ctx, "looking up identifier for identity as label [%s]", utils.Hashable(id))

	label := string(id)
	if info, err := r.localMembership.GetIdentityInfo(ctx, label, nil); err == nil {
		return nil, info.ID(), nil
	}
	if idIdentifier, err := r.localMembership.GetIdentifier(ctx, id); err == nil {
		return nil, idIdentifier, nil
	}

	r.logger.DebugfContext(ctx, "cannot find match for driver.Identity string [%s]", id)

	return nil, string(id), nil
}
