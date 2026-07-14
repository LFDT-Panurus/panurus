/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"encoding/json"
	"testing"

	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/stretchr/testify/require"
)

func mustEnvelopeBytes(t *testing.T, msgType string, body any) []byte {
	t.Helper()

	env, err := jsession.WrapEnvelope(body, msgType)
	require.NoError(t, err)
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	return raw
}

func mustUnwrapBody(t *testing.T, wire []byte, expectedType string) []byte {
	t.Helper()

	env, err := jsession.UnwrapEnvelope(wire, expectedType)
	require.NoError(t, err)

	return env.Body
}
