/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"github.com/LFDT-Panurus/panurus/token"
)

// PublicParameters represents the public configuration data for a
// specific token management system (TMS). It includes the unique TMS
// identifier, the ledger path where parameters are stored,
// and the raw byte content of the parameters.
type PublicParameters struct {
	// TMSID is the ID of the token management system.
	TMSID token.TMSID
	// Path is the path to the public parameters.
	Path string
	// Raw is the raw public parameters.
	Raw []byte
}
