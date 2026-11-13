/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// TestIssueService_ValuesOwnersMismatch checks that Issue rejects a number of
// values that differs from the number of recipients before any proving. The
// orchestrator cannot catch it: it only sees the (value, owner) pairs built by
// Issue. The service has no orchestrator, so reaching the prover would panic.
func TestIssueService_ValuesOwnersMismatch(t *testing.T) {
	s := zkatsnark.NewIssueService(logging.MustGetLogger(), &upgradePPM{pp: pp.DefaultPublicParams()}, nil, nil, nil, nil, nil, nil)
	issuer := driver.Identity("issuer")

	tests := []struct {
		name    string
		values  []uint64
		owners  [][]byte
		wantErr string
	}{
		{
			name:    "more values than owners",
			values:  []uint64{1, 2},
			owners:  [][]byte{[]byte("alice")},
			wantErr: "number of values [2] does not match number of recipients [1]",
		},
		{
			name:    "more owners than values",
			values:  []uint64{1},
			owners:  [][]byte{[]byte("alice"), []byte("bob")},
			wantErr: "number of values [1] does not match number of recipients [2]",
		},
		{
			// with a non-None issuer this is not the upgrade path, so nil
			// values are now rejected by the length check
			name:    "nil values with an issuer",
			values:  nil,
			owners:  [][]byte{[]byte("alice")},
			wantErr: "number of values [0] does not match number of recipients [1]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, _, err = s.Issue(t.Context(), issuer, "ABC", tc.values, tc.owners, nil)
			})
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

// TestIssueService_ValueValidation checks that Issue rejects zero values,
// values exceeding MaxTokenValue, and total values that overflow uint64.
func TestIssueService_ValueValidation(t *testing.T) {
	s := zkatsnark.NewIssueService(logging.MustGetLogger(), &upgradePPM{pp: pp.DefaultPublicParams()}, nil, nil, nil, nil, nil, nil)
	issuer := driver.Identity("issuer")

	t.Run("zero value", func(t *testing.T) {
		_, _, err := s.Issue(t.Context(), issuer, "ABC", []uint64{0}, [][]byte{[]byte("alice")}, nil)
		require.EqualError(t, err, "value [0] outside valid range [1, 18446744073709551615]")
	})

	t.Run("value exceeding max token value", func(t *testing.T) {
		p16 := pp.DefaultPublicParams()
		p16.MaxBits = 16
		s16 := zkatsnark.NewIssueService(logging.MustGetLogger(), &upgradePPM{pp: p16}, nil, nil, nil, nil, nil, nil)
		_, _, err := s16.Issue(t.Context(), issuer, "ABC", []uint64{65536}, [][]byte{[]byte("alice")}, nil)
		require.EqualError(t, err, "value [65536] outside valid range [1, 65535]")
	})

	t.Run("overflow total value", func(t *testing.T) {
		_, _, err := s.Issue(t.Context(), issuer, "ABC", []uint64{math.MaxUint64, 1}, [][]byte{[]byte("alice"), []byte("bob")}, nil)
		require.EqualError(t, err, "total issue value overflows uint64")
	})
}
