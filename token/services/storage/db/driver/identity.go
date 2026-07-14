/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
)

type (
	WalletID              = idriver.WalletID
	IdentityConfiguration = driver.IdentityConfiguration
	WalletStore           = idriver.WalletStoreService
	IdentityStore         = idriver.IdentityStoreService
	KeyStore              = idriver.Keystore
)
