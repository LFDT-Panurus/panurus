/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
	"github.com/stretchr/testify/require"
)

// The application-data validators must count only keys in the public namespace,
// so that any other metadata key is rejected by the common validator as unvalidated.
func TestApplicationDataValidate_CountsOnlyPublicKeys(t *testing.T) {
	t.Parallel()

	md := map[string][]byte{meta.PublicMetadataPrefix + "a": []byte("1"), "private": []byte("2")}

	tctx := &validator.Context{TransferAction: &token.TransferAction{Metadata: md}}
	require.NoError(t, common.TransferApplicationDataValidate[*pp.PublicParams, *token.Input, *token.TransferAction, *token.IssueAction, driver.Deserializer](context.Background(), tctx))
	require.Equal(t, map[string]int{meta.PublicMetadataPrefix + "a": 1}, tctx.MetadataCounter)

	ictx := &validator.Context{IssueAction: &token.IssueAction{Metadata: md}}
	require.NoError(t, common.IssueApplicationDataValidate[*pp.PublicParams, *token.Input, *token.TransferAction, *token.IssueAction, driver.Deserializer](context.Background(), ictx))
	require.Equal(t, map[string]int{meta.PublicMetadataPrefix + "a": 1}, ictx.MetadataCounter)
}
