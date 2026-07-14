/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/identity/config"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	config3 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvalidConf(t *testing.T) {
	cp, err := config3.NewProvider("./testdata/invalid")
	require.NoError(t, err)
	tms := config2.NewConfiguration(cp, "n1c1ns1", driver.TMSID{})
	_, err = config.NewIdentityConfig(tms)
	require.Error(t, err, "should have failed due to invalid config")
}

func TestCacheSizeIdentityConfig(t *testing.T) {
	cp, err := config3.NewProvider("./testdata/token0")
	require.NoError(t, err)
	tms := config2.NewConfiguration(cp, "n1c1ns1", driver.TMSID{})
	identityConfig, err := config.NewIdentityConfig(tms)
	require.NoError(t, err, "failed creating identity config")
	assert.Equal(t, 3, identityConfig.DefaultCacheSize(), "default cache size should be 3")
	assert.Equal(t, 5, identityConfig.CacheSizeForOwnerID("owner1"), "alice cache size should be 5")
	assert.Equal(t, 3, identityConfig.CacheSizeForOwnerID("unknown"))
}

func TestTranslatePath(t *testing.T) {
	cp, err := config3.NewProvider("./testdata/token0")
	require.NoError(t, err)
	tms := config2.NewConfiguration(cp, "n1c1ns1", driver.TMSID{})
	identityConfig, err := config.NewIdentityConfig(tms)
	require.NoError(t, err, "failed creating identity config")
	assert.Contains(t, identityConfig.TranslatePath("./testdata/token0"), "panurus", "translate path should contain panurus")
}

func TestIdentitiesForRole(t *testing.T) {
	cp, err := config3.NewProvider("./testdata/token0")
	require.NoError(t, err)
	tms := config2.NewConfiguration(cp, "n1c1ns1", driver.TMSID{})
	identityConfig, err := config.NewIdentityConfig(tms)
	require.NoError(t, err, "failed creating identity config")

	identities, err := identityConfig.IdentitiesForRole(idriver.OwnerRole)
	require.NoError(t, err, "failed getting identities for owner role")
	assert.Len(t, identities, 1, "should have 1 owner identity")
	for i, identity := range identities {
		index := i + 1
		assert.Equal(t, fmt.Sprintf("owner%d", index), identity.ID, "id should have been owner%d", index)
		assert.Equal(t, fmt.Sprintf("owner%d", index), identity.String(), "id should have been owner%d", index)
		assert.Equal(t, fmt.Sprintf("/path/to/crypto/owner%d", index), identity.Path, "path should have been path /path/to/crypto/owner%d", index)
	}

	identities, err = identityConfig.IdentitiesForRole(idriver.IssuerRole)
	require.NoError(t, err, "failed getting identities for issuer role")
	assert.Len(t, identities, 2, "should have 2 issuer identity")
	iss, err := crypto.ToBCCSPOpts(identities[1].Opts)
	require.NoError(t, err, "failed converting to bccsp opts")
	assert.Equal(t, "SW", iss.Default)
	assert.Equal(t, "1234", iss.PKCS11.Pin)
	assert.Equal(t, 256, iss.SW.Security)

	identities, err = identityConfig.IdentitiesForRole(idriver.AuditorRole)
	require.NoError(t, err, "failed getting identities for auditor role")
	assert.Len(t, identities, 3, "should have 3 auditor identity")

	identities, err = identityConfig.IdentitiesForRole(idriver.CertifierRole)
	require.NoError(t, err, "failed getting identities for certifier role")
	assert.Len(t, identities, 4, "should have 4 certifier identity")

	_, err = identityConfig.IdentitiesForRole(1234)
	require.Error(t, err, "should throw identity for invalid role (1234)")
	assert.Equal(t, "unknown role [1234]", err.Error(), "should throw identity for invalid role (1234)")
}
