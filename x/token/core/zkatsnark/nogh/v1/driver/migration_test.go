/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
)

// TestNewTokensServices_DLogRouting checks that, with embedded ZKAT-DLOG
// public parameters, the ZKAT-DLOG formats are routed to the upgrade service
// only, and that without them the upgrade service refuses them.
func TestNewTokensServices_DLogRouting(t *testing.T) {
	p := pp.DefaultPublicParams()
	legacy, err := dlogpp.Embed(p, dlogpp.DefaultCurve)
	require.NoError(t, err)
	p.PKMigration, p.VKMigration = []byte("pk"), []byte("vk") // routing only, no proving
	formats, err := dlogtoken.CommTokenFormats(legacy)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		p        *pp.PublicParams
		eligible bool
	}{
		{"with ZKAT-DLOG public parameters", p, true},
		{"without", pp.DefaultPublicParams(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ppm, err := common.NewPublicParamsManagerFromParams(tc.p)
			require.NoError(t, err)
			tokensService, upgradeService, err := zkatsnarkdriver.NewTokensServices(logging.MustGetLogger(), ppm, nil, nil)
			require.NoError(t, err)
			if !tc.eligible {
				require.Nil(t, upgradeService.PublicParamsHistory, "no ZKAT-DLOG format is eligible")
			}
			for _, format := range formats {
				require.False(t, slices.Contains(tokensService.SupportedTokenFormats(), format), "never spendable directly")
				if tc.eligible {
					_, _, err := upgradeService.PublicParamsHistory.ByFormat(t.Context(), format)
					require.NoError(t, err, "format [%s]", format)
				}
			}
		})
	}
}
