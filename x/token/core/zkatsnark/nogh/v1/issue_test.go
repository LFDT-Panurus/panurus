/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
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
