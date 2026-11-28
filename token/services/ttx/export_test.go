/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import "time"

// SetEndorseViewReceiveBudget overrides the signature-collection budget of v.
func SetEndorseViewReceiveBudget(v *EndorseView, d time.Duration) {
	v.receiveBudget = d
}
