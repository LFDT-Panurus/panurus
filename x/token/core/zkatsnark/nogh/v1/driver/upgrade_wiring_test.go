/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"slices"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
)

const idemixTestdataPath = "../../../../../../../token/core/zkatdlog/nogh/v1/testdata"

// upgradeConfiguration builds a BN254 setup configuration whose circuits are
// compiled for maxBits.
func upgradeConfiguration(t *testing.T, maxBits int) *testutils.SetupConfiguration {
	t.Helper()
	configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           []ecc.ID{ecc.BN254},
		OwnerIdentityType:  idemixnym.IdentityType,
		MaxBits:            maxBits,
	})
	require.NoError(t, err)
	configuration, err := configurations.GetSetupConfiguration(ecc.BN254)
	require.NoError(t, err)

	return configuration
}

// TestNewTokensServices_UpgradePaths checks, on the services wired as
// NewTokenService wires them (NewTokensServices), that every fabtoken format
// takes exactly one upgrade path: in place (TokensService) or through the
// issuer (TokensUpgradeService). With MaxBits = 32 it also runs one
// burn-and-re-issue of a 64-bit fabtoken through that wiring, and checks that a
// 32-bit one, spendable in place, is refused by the issuer.
func TestNewTokensServices_UpgradePaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		maxBits int
		inPlace []uint64
		issuer  []uint64
	}{
		{name: "default", maxBits: 0, inPlace: []uint64{16, 32, 64}},
		{name: "MaxBits=32", maxBits: 32, inPlace: []uint64{16, 32}, issuer: []uint64{64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration := upgradeConfiguration(t, tc.maxBits)
			require.Equal(t, uint64(64), configuration.PP.PrecisionValue)
			ppm, err := common.NewPublicParamsManagerFromParams(configuration.PP)
			require.NoError(t, err)
			deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
			require.NoError(t, err)

			tokensService, upgradeService, err := zkatsnarkdriver.NewTokensServices(logging.MustGetLogger(), ppm, deserializer, nil)
			require.NoError(t, err)
			direct := tokensService.SupportedTokenFormats()
			eligible := upgradeService.UpgradeSupportedTokenFormatList
			for _, precision := range []uint64{16, 32, 64} {
				format, err := fabtoken.SupportedTokenFormat(precision)
				require.NoError(t, err)
				require.Equal(t, slices.Contains(tc.inPlace, precision), slices.Contains(direct, format), "precision [%d] in place", precision)
				require.Equal(t, slices.Contains(tc.issuer, precision), slices.Contains(eligible, format), "precision [%d] through the issuer", precision)
				require.NotEqual(t, slices.Contains(direct, format), slices.Contains(eligible, format), "precision [%d] must take exactly one path", precision)
			}

			if tc.maxBits == 0 {
				return
			}
			f, err := testutils.NewUpgradeFixtureWithOptions(configuration, testutils.UpgradeOptions{
				Quantities:        []string{"0x10", "0x20"},
				FabtokenPrecision: 64,
				DriverWiring:      true,
			})
			require.NoError(t, err)
			tr, md, err := f.Request()
			require.NoError(t, err)
			require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
			signed, err := f.Sign("tx")
			require.NoError(t, err)
			require.NoError(t, f.Verify("tx", signed))

			_, err = testutils.NewUpgradeFixtureWithOptions(configuration, testutils.UpgradeOptions{
				Quantities:        []string{"0x10"},
				FabtokenPrecision: 32,
				DriverWiring:      true,
			})
			require.ErrorContains(t, err, "upgrade of already supported token format")
		})
	}
}
