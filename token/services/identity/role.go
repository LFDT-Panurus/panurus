/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
)

// RoleType is the role of an identity
type RoleType = driver.IdentityRoleType

const (
	// IssuerRole is the role of an issuer
	IssuerRole RoleType = driver.IssuerRole
	// AuditorRole is the role of an auditor
	AuditorRole = driver.AuditorRole
	// OwnerRole is the role of an owner
	OwnerRole = driver.OwnerRole
	// CertifierRole is the role of a certifier
	CertifierRole = driver.CertifierRole
)
