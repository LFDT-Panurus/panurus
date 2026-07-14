/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueApplicationDataValidate(t *testing.T) {
	tests := []struct {
		name    string
		err     bool
		errMsg  string
		context func() (*TestContext, TestCheck)
	}{
		{
			name: "no metadata",
			err:  false,
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns(nil)
				action := &mock.IssueAction{}
				action.GetMetadataReturns(nil)
				ctx := &TestContext{
					PP:          pp,
					IssueAction: action,
				}

				return ctx, func() bool {
					return len(ctx.MetadataCounter) == 0
				}
			},
		},
		{
			name: "one metadata without the selected prefix",
			err:  false,
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns(nil)
				action := &mock.IssueAction{}
				action.GetMetadataReturns(map[string][]byte{
					"key1": []byte("value1"),
				})
				ctx := &TestContext{
					PP:          pp,
					IssueAction: action,
				}

				return ctx, func() bool {
					return len(ctx.MetadataCounter) == 0
				}
			},
		},
		{
			name: "one metadata with the selected prefix",
			err:  false,
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns(nil)
				action := &mock.IssueAction{}
				k := meta.PublicMetadataPrefix + "key1"
				action.GetMetadataReturns(map[string][]byte{
					k:             []byte("value1"),
					"another key": []byte("value2"),
				})
				ctx := &TestContext{
					PP:              pp,
					IssueAction:     action,
					MetadataCounter: map[MetadataCounterID]int{},
				}

				return ctx, func() bool {
					return len(ctx.MetadataCounter) == 1 && ctx.MetadataCounter[k] == 1
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, check := tt.context()
			err := IssueApplicationDataValidate(t.Context(), ctx)
			if tt.err {
				require.Error(t, err)
				require.EqualError(t, err, tt.errMsg)
			} else {
				require.NoError(t, err)
			}
			if check != nil {
				assert.True(t, check())
			}
		})
	}
}
