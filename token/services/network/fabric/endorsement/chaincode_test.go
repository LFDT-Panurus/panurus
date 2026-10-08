/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement_test

import (
	"strings"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tmsKeyID = "id1"

var testTMSID = token2.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}

// newConfigService returns a configuration service holding a single TMS whose endorser
// selection keys are served by mspIDs.
func newConfigService(mspIDs []string) *config.Service {
	cp := &mocks.Provider{}
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		switch {
		case key == "token.tms":
			*rawVal.(*map[string]any) = map[string]any{tmsKeyID: nil}
		case key == "token.tms."+tmsKeyID:
			*rawVal.(*driver.TMSID) = testTMSID
		case strings.HasSuffix(key, config3.EndorsersMSPIDsKey):
			*rawVal.(*[]string) = mspIDs
		}

		return nil
	}

	return config.NewService(cp)
}

// newServiceProvider builds a ServiceProvider with only the configuration service wired.
// The chaincode-endorsement branch of the loader uses nothing else.
func newServiceProvider(configService *config.Service) *endorsement.ServiceProvider {
	return endorsement.NewServiceProvider(nil, nil, configService, nil, nil, nil, nil, nil, nil)
}

// TestChaincodeEndorsementServiceReadsSelection confirms the endorser selection
// configured for a TMS reaches the chaincode endorsement service, which is what keeps
// Token Chaincode endorsement on the chosen organizations' peers.
func TestChaincodeEndorsementServiceReadsSelection(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mspIDs []string
		want   config3.EndorserSelection
	}{
		{
			name: "no preference configured",
			want: config3.EndorserSelection{},
		},
		{
			name:   "a single organization",
			mspIDs: []string{"Org1MSP"},
			want:   config3.EndorserSelection{MSPIDs: []string{"Org1MSP"}},
		},
		{
			name:   "several organizations",
			mspIDs: []string{"Org1MSP", "Org3MSP"},
			want:   config3.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, err := newServiceProvider(newConfigService(tt.mspIDs)).Get(testTMSID)
			require.NoError(t, err)

			ccService, ok := service.(*endorsement.ChaincodeEndorsementService)
			require.True(t, ok, "expected a chaincode endorsement service, got %T", service)
			assert.Equal(t, testTMSID, ccService.TMSID)
			assert.Equal(t, tt.want, ccService.EndorserSelection)
		})
	}
}

// TestChaincodeEndorsementServiceRejectsInvalidSelection confirms a TMS whose selection
// cannot be honoured fails to load, rather than silently falling back to an endorser set
// the operator did not ask for.
func TestChaincodeEndorsementServiceRejectsInvalidSelection(t *testing.T) {
	_, err := newServiceProvider(newConfigService([]string{"Org1MSP", "Org1MSP"})).Get(testTMSID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than once")
}

// fakeEndorseView stands in for the FSC endorse view, recording the endorser restriction
// applied to it. It returns itself, so it satisfies config.EndorserSelectable the same way
// the real view does.
//
// The real view type is unexported in FSC and its Endorse needs a live network, so this is
// how the endorse path's restriction is observed.
type fakeEndorseView struct {
	mspIDs []string
}

func (v *fakeEndorseView) WithEndorsersByMSPIDs(mspIDs ...string) *fakeEndorseView {
	v.mspIDs = mspIDs

	return v
}

// TestEndorseAppliesEndorserSelection covers the step Endorse takes between building its
// invocation and submitting it: the configured selection is applied to the view, which is
// what actually keeps the approval request on the chosen peers.
func TestEndorseAppliesEndorserSelection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		selection  config3.EndorserSelection
		wantMSPIDs []string
	}{
		{
			name:      "no selection leaves default discovery",
			selection: config3.EndorserSelection{},
		},
		{
			name:       "mspIDs reach the invocation",
			selection:  config3.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}},
			wantMSPIDs: []string{"Org1MSP", "Org3MSP"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := endorsement.NewChaincodeEndorsementService(testTMSID, tt.selection)

			view := &fakeEndorseView{}
			got := endorsement.WithEndorserSelectionForTest(service, view)

			assert.Same(t, view, got)
			assert.Equal(t, tt.wantMSPIDs, view.mspIDs)
		})
	}
}

// TestEndorsementErrorNamesTheRestriction covers the diagnostic wrapping of a failed
// endorsement. A restriction no peer satisfies — a misspelled MSP ID, or an organization
// whose peers cannot satisfy the chaincode's endorsement policy — otherwise surfaces as a
// discovery failure naming neither an endorser nor the key responsible.
func TestEndorsementErrorNamesTheRestriction(t *testing.T) {
	cause := errors.New("no endorsers found")

	t.Run("an unrestricted endorsement's error is untouched", func(t *testing.T) {
		service := endorsement.NewChaincodeEndorsementService(testTMSID, config3.EndorserSelection{})

		err := service.EndorsementErrorForTest(cause)
		require.ErrorIs(t, err, cause)
		assert.Equal(t, cause.Error(), err.Error())
	})

	t.Run("mspIDs are named along with their key", func(t *testing.T) {
		service := endorsement.NewChaincodeEndorsementService(
			testTMSID,
			config3.EndorserSelection{MSPIDs: []string{"Org9MSP"}},
		)

		err := service.EndorsementErrorForTest(cause)
		require.ErrorIs(t, err, cause)
		assert.Contains(t, err.Error(), config3.EndorsersMSPIDsKey)
		assert.Contains(t, err.Error(), "Org9MSP")
		assert.Contains(t, err.Error(), "no endorsers found")
		assert.Contains(t, err.Error(), testTMSID.String())
	})
}
