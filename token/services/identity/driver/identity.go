/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
)

type (
	// IdentityType identifies the type of identity.
	// It is an alias for driver.IdentityType and is used by deserializers to choose the correct
	// decoding logic for different identity representations.
	IdentityType = driver.IdentityType

	// IdentityTypeString is an alias for driver.IdentityTypeString
	IdentityTypeString = driver.IdentityTypeString

	// Identity is an alias for driver.Identity
	Identity = driver.Identity
)
