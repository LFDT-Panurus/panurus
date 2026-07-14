/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import "github.com/LFDT-Panurus/panurus/token/driver"

// Authorization defines method to check the relation between a token
// and wallets (owner, auditor, etc.)
type Authorization struct {
	driver.Authorization
}
