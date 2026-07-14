/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"fmt"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
)

// IdentityRoleType is the role of an identity
type IdentityRoleType int

const (
	// IssuerRole is the role of an issuer
	IssuerRole IdentityRoleType = iota
	// AuditorRole is the role of an auditor
	AuditorRole
	// OwnerRole is the role of an owner
	OwnerRole
	// CertifierRole is the role of a certifier
	CertifierRole
)

var (
	IdentityRoleStrings = map[IdentityRoleType]string{
		IssuerRole:    "issuer",
		AuditorRole:   "auditor",
		OwnerRole:     "owner",
		CertifierRole: "certifier",
	}
)

func RoleToString(r IdentityRoleType) string {
	s, ok := IdentityRoleStrings[r]
	if ok {
		return s
	}

	return fmt.Sprintf("role%d", r)
}

// IdentityInfo models a long-term identity inside the Identity Provider.
// An identity has an identifier (ID) and an Enrollment ID, unique identifier.
// An identity can be remote, meaning that the corresponding secret key is remotely available.
type IdentityInfo interface {
	// ID returns the identifier of the Identity
	ID() string
	// EnrollmentID returns the enrollment ID of the Identity
	EnrollmentID() string
	// Remote is true if this identity info refers to an identify whose corresponding secret key is not known, it is external/remote
	Remote() bool
	// Get returns the identity and it is audit info.
	// Get might return a different identity at each call depending on the implementation.
	Get(ctx context.Context) (Identity, []byte, error)
	// Anonymous is true if this identity supports anonymity
	Anonymous() bool
}

type (
	// WalletLookupID defines the type of identifiers that can be used to retrieve a given wallet.
	// It can be a string, as the name of the wallet, or an identity contained in that wallet.
	// Ultimately, it is the token tdriver to decide which types are allowed.
	WalletLookupID = tdriver.WalletLookupID
	// IdentityConfiguration contains configuration-related information of an identity
	IdentityConfiguration = tdriver.IdentityConfiguration
)

// Role is a container of long-term identities.
// A long-term identity is then used to construct a wallet.
//
//go:generate counterfeiter -o mock/role.go -fake-name Role . Role
type Role interface {
	// ID returns the identifier of this role
	ID() IdentityRoleType
	// MapToIdentity returns the long-term identity and its identifier for the given index.
	// The index can be an identity or a label (string).
	MapToIdentity(ctx context.Context, v WalletLookupID) (Identity, string, error)
	// GetIdentityInfo returns the long-term identity info associated to the passed id
	GetIdentityInfo(ctx context.Context, id string) (IdentityInfo, error)
	// RegisterIdentity registers the given identity
	RegisterIdentity(ctx context.Context, config IdentityConfiguration) error
	// IdentityIDs returns the identifiers contained in this role
	IdentityIDs() ([]string, error)
	// Done releases all the resources allocated by this service.
	Done() error
}
