/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package topology

import (
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
)

type Identity struct {
	ID      string
	Type    string
	Path    string
	Default bool
	Opts    *crypto.BCCSP
	Raw     []byte
}

type Wallets struct {
	Certifiers []Identity
	Issuers    []Identity
	Owners     []Identity
	Auditors   []Identity
}
