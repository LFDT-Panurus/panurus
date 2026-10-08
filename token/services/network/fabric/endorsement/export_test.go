/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
)

// WithEndorserSelectionForTest exposes withEndorserSelection to the package's external
// tests. It is the seam through which the restriction Endorse puts on its invocation is
// covered, Endorse itself needing a live Fabric network.
func WithEndorserSelectionForTest[T config.EndorserSelectable[T]](e *ChaincodeEndorsementService, v T) T {
	return withEndorserSelection(e, v)
}

// EndorsementErrorForTest exposes endorsementError to the package's external tests.
func (e *ChaincodeEndorsementService) EndorsementErrorForTest(err error) error {
	return e.endorsementError(err)
}
