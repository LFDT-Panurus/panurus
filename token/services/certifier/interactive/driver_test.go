/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive_test

import (
	"testing"

	token "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/require"
)

// TestDriver_NewCertificationService_BackendFactoryError verifies that when
// the BackendFactory returns an error, NewCertificationService propagates it
// and leaves CertificationService nil.
func TestDriver_NewCertificationService_BackendFactoryError(t *testing.T) {
	factoryErr := errors.New("backend init failed")

	d := interactive.NewDriver(
		func(_ *token.ManagementService, _ string) (interactive.Backend, error) {
			return nil, factoryErr
		},
		nil, nil, &fakeViewManager{},
		&mock.ResponderRegistryMock{},
		&disabled.Provider{},
	)

	_, err := d.NewCertificationService(nil, "wallet")
	require.Error(t, err)
	require.ErrorIs(t, err, factoryErr)
	require.Nil(t, d.CertificationService, "service must not be set after factory failure")
}
