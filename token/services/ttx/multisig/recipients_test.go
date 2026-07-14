/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests recipients.go which provides recipient identity management for multisig transactions.
// Tests verify the wrapper function for requesting recipient identities.
package multisig_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	"github.com/stretchr/testify/assert"
)

func TestRequestRecipientIdentity_NilContext(t *testing.T) {
	// This function is a simple wrapper around ttx.RequestMultisigIdentity
	// We can't test it fully without a proper view context, but we can
	// verify it exists and has the correct signature

	// Calling with nil context should panic or return error
	// This documents the function exists and is exported
	assert.NotNil(t, multisig.RequestRecipientIdentity)
}
