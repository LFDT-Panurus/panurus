/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// TestBaseWalletServiceFactory_InvalidPublicParams checks that public
// parameters of the wrong type, or nil, are rejected with an error instead of
// a panic.
func TestBaseWalletServiceFactory_InvalidPublicParams(t *testing.T) {
	tests := []struct {
		name string
		pp   driver.PublicParameters
	}{
		{name: "foreign type", pp: &mock.PublicParameters{}},
		{name: "untyped nil", pp: nil},
		{name: "typed nil", pp: (*pp.PublicParams)(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &zkatsnarkdriver.BaseWalletServiceFactory{}
			var ws any
			var err error
			require.NotPanics(t, func() {
				ws, err = f.NewWalletService(nil, nil, nil, nil, nil, nil, nil, tc.pp, false, nil)
			})
			require.Error(t, err)
			require.ErrorContains(t, err, "invalid public parameters type")
			require.Nil(t, ws)
		})
	}
}
