/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

const maxFuzzPublicParamsBytes = 256 << 10

// FuzzPublicParamsDeserializeNoPanic fuzzes pp.DeserializePublicParams with
// arbitrary bytes. Public parameters are read from the ledger by validators
// and provers, so any panic during deserialization is an unauthenticated DoS.
func FuzzPublicParamsDeserializeNoPanic(f *testing.F) {
	orig := defaultValidPP()
	raw, err := orig.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzPublicParamsBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = pp.DeserializePublicParams(raw)
		})
	})
}
