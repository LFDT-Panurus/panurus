/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

// RecipientRegistrationRollback is implemented by identity providers that record
// side effects in RegisterRecipientIdentity before RegisterRecipientData.
// If RegisterRecipientData never succeeds, the implementation should undo those
// effects so the node is not left in a half-registered state.
type RecipientRegistrationRollback interface {
	RollbackPartialRecipientRegistration(ctx context.Context, id driver.Identity)
}
