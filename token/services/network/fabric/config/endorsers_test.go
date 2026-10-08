/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConfiguration serves the endorser-selection key, and optionally fails the mspIDs
// unmarshalling.
type fakeConfiguration struct {
	mspIDs         []string
	unmarshalError error
	// fscEndorsement makes IsSet report the FSC endorsement key as present, which is how
	// ResolveEndorserSelection recognises the mode the selection does not apply to.
	fscEndorsement bool
}

func (f *fakeConfiguration) IsSet(key string) bool {
	return f.fscEndorsement && key == config.FSCEndorsementKey
}

func (f *fakeConfiguration) UnmarshalKey(key string, rawVal any) error {
	if f.unmarshalError != nil {
		return f.unmarshalError
	}
	if key != config.EndorsersMSPIDsKey {
		return nil
	}
	dst, ok := rawVal.(*[]string)
	if !ok {
		return errors.Errorf("unexpected destination type %T", rawVal)
	}
	*dst = f.mspIDs

	return nil
}

func TestLoadEndorserSelection(t *testing.T) {
	t.Run("unset configuration selects nothing", func(t *testing.T) {
		selection, err := config.LoadEndorserSelection(&fakeConfiguration{})
		require.NoError(t, err)
		assert.False(t, selection.IsSet())
		assert.Empty(t, selection.MSPIDs)
	})

	t.Run("mspIDs only", func(t *testing.T) {
		selection, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP", "Org3MSP"},
		})
		require.NoError(t, err)
		assert.True(t, selection.IsSet())
		assert.Equal(t, []string{"Org1MSP", "Org3MSP"}, selection.MSPIDs)
	})

	t.Run("empty MSP ID is rejected", func(t *testing.T) {
		_, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP", ""},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty MSP ID")
	})

	t.Run("a blank MSP ID is rejected", func(t *testing.T) {
		// A whitespace-only entry is a typo that would otherwise reach Fabric and strand
		// the invocation with an endorser set nothing can satisfy.
		_, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP", "   "},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty MSP ID")
	})

	t.Run("surrounding whitespace is trimmed before the value is stored", func(t *testing.T) {
		// Validating a trimmed value while storing the raw one would send " Org1MSP" to
		// discovery, which matches no MSP — the endorser-less discovery failure this
		// selection exists to prevent.
		selection, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{" Org1MSP", "Org3MSP\t"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"Org1MSP", "Org3MSP"}, selection.MSPIDs)
	})

	t.Run("a duplicate that differs only by whitespace is rejected", func(t *testing.T) {
		_, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP", "Org1MSP "},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
	})

	t.Run("an unset selection keeps a nil MSPIDs, not an empty slice", func(t *testing.T) {
		// Callers compare against the zero value, which an allocated empty slice would
		// no longer equal.
		selection, err := config.LoadEndorserSelection(&fakeConfiguration{})
		require.NoError(t, err)
		assert.Equal(t, config.EndorserSelection{}, selection)
		assert.Nil(t, selection.MSPIDs)
	})

	t.Run("a repeated MSP ID is rejected", func(t *testing.T) {
		// Listing an organization twice expresses nothing a single entry does not, so it
		// is a copy-paste mistake worth naming at startup.
		_, err := config.LoadEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP", "Org3MSP", "Org1MSP"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
		assert.Contains(t, err.Error(), "Org1MSP")
	})

	t.Run("unmarshalling failure is propagated", func(t *testing.T) {
		_, err := config.LoadEndorserSelection(&fakeConfiguration{
			unmarshalError: errors.New("boom"),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
		assert.Contains(t, err.Error(), config.EndorsersMSPIDsKey)
	})
}

func TestEndorserSelectionIsSet(t *testing.T) {
	assert.False(t, config.EndorserSelection{}.IsSet())
	assert.True(t, config.EndorserSelection{MSPIDs: []string{"Org1MSP"}}.IsSet())
}

// fakeSelectable records the calls ApplyEndorserSelection makes, returning itself so it
// satisfies EndorserSelectable.
type fakeSelectable struct {
	mspIDs []string
	calls  int
}

func (f *fakeSelectable) WithEndorsersByMSPIDs(mspIDs ...string) *fakeSelectable {
	f.mspIDs = mspIDs
	f.calls++

	return f
}

func TestApplyEndorserSelection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		selection  config.EndorserSelection
		wantMSPIDs []string
		wantCalls  int
	}{
		{
			name:      "unset selection touches nothing",
			selection: config.EndorserSelection{},
		},
		{
			name:       "mspIDs",
			selection:  config.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}},
			wantMSPIDs: []string{"Org1MSP", "Org3MSP"},
			wantCalls:  1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &fakeSelectable{}
			got := config.ApplyEndorserSelection(v, tt.selection)

			assert.Same(t, v, got)
			assert.Equal(t, tt.wantMSPIDs, v.mspIDs)
			assert.Equal(t, tt.wantCalls, v.calls)
		})
	}
}

// TestExplain covers the rendering a failed invocation is annotated with. The MSP IDs
// cannot be validated when the configuration is loaded — the configuration layer has no
// channel, so it cannot know which of them exist or which host peers — which makes
// quoting the restriction in force the only diagnosis an operator gets.
func TestExplain(t *testing.T) {
	t.Run("an unset selection restricts nothing", func(t *testing.T) {
		assert.Equal(t, "endorsement is not restricted to any organization", config.EndorserSelection{}.Explain())
	})

	t.Run("mspIDs name their key and their value", func(t *testing.T) {
		explanation := config.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}}.Explain()
		assert.Contains(t, explanation, config.EndorsersMSPIDsKey)
		assert.Contains(t, explanation, "Org1MSP, Org3MSP")
	})
}

// TestResolveEndorserSelection covers the single place that decides whether a configured
// selection applies, shared by the endorsement service loader and the public-parameters
// fetcher so the two cannot drift.
func TestResolveEndorserSelection(t *testing.T) {
	t.Run("chaincode endorsement applies the selection", func(t *testing.T) {
		selection, chaincodeEndorsement, err := config.ResolveEndorserSelection(&fakeConfiguration{
			mspIDs: []string{"Org1MSP"},
		})
		require.NoError(t, err)
		assert.True(t, chaincodeEndorsement)
		assert.Equal(t, config.EndorserSelection{MSPIDs: []string{"Org1MSP"}}, selection)
	})

	t.Run("FSC endorsement does not apply the selection", func(t *testing.T) {
		// The configured selection is still returned, so a caller can report the
		// selection it is about to ignore.
		selection, chaincodeEndorsement, err := config.ResolveEndorserSelection(&fakeConfiguration{
			mspIDs:         []string{"Org1MSP"},
			fscEndorsement: true,
		})
		require.NoError(t, err)
		assert.False(t, chaincodeEndorsement)
		assert.Equal(t, []string{"Org1MSP"}, selection.MSPIDs)
	})

	t.Run("an invalid selection is rejected in either mode", func(t *testing.T) {
		for _, fscEndorsement := range []bool{false, true} {
			_, _, err := config.ResolveEndorserSelection(&fakeConfiguration{
				mspIDs:         []string{"Org1MSP", "Org1MSP"},
				fscEndorsement: fscEndorsement,
			})
			require.Error(t, err, "fscEndorsement=%v", fscEndorsement)
			assert.Contains(t, err.Error(), "more than once")
		}
	})
}

// TestNoUsableEndorserError covers the annotation a failed restricted invocation carries.
// Both the endorse path and the public-parameters query share it, so a reworded fix
// cannot leave the two inconsistent.
func TestNoUsableEndorserError(t *testing.T) {
	cause := errors.New("boom")
	restriction := config.EndorserSelection{MSPIDs: []string{"Org9MSP"}}.Explain()
	err := config.NoUsableEndorserError(cause, "endorse for [n1:c1:ns1]", restriction)

	require.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "failed to endorse for [n1:c1:ns1], where ")
	assert.Contains(t, err.Error(), config.EndorsersMSPIDsKey)
	assert.Contains(t, err.Error(), "Org9MSP")
	assert.Contains(t, err.Error(), "boom")
	assert.Contains(t, err.Error(), "host peers able to satisfy the chaincode endorsement policy")
}
