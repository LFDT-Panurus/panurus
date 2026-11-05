/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateForDeployment(t *testing.T) {
	t.Run("nil issuers warns", func(t *testing.T) {
		p := DefaultPublicParams()
		p.IssuerIdentities = nil
		require.NoError(t, p.Validate(), "open-policy mode must remain valid")
		err := p.ValidateForDeployment()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "open-policy mode")
	})

	t.Run("empty issuers warns", func(t *testing.T) {
		p := DefaultPublicParams()
		p.IssuerIdentities = []driver.Identity{}
		require.NoError(t, p.Validate())
		err := p.ValidateForDeployment()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "IssuerIdentities is empty")
	})

	t.Run("issuers set", func(t *testing.T) {
		p := DefaultPublicParams()
		p.AddIssuer(driver.Identity("issuer"))
		require.NoError(t, p.ValidateForDeployment())
	})
}
