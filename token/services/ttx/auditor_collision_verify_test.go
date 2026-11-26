/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"reflect"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscview "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type baseContext = view.Context

// registryContext is a view.Context whose only working method is GetService, which
// returns the real FSC registry.
type registryContext struct {
	baseContext
	reg *fscview.Registry
}

func (c *registryContext) GetService(v any) (any, error) {
	if v == reflect.TypeFor[*fscview.Registry]() {
		return c.reg, nil
	}

	return nil, errors.Errorf("service [%v] not found", v)
}

type auditViewStub struct{ tms string }

func (*auditViewStub) Call(view.Context) (any, error) { return nil, nil }

// TestVerify_PerTMSAuditorsDoNotCollide asserts the desired behaviour: registering the
// auditor view of a second TMS (ttx/auditor.go:146) must not silently replace the first
// TMS's auditor view.
func TestVerify_PerTMSAuditorsDoNotCollide(t *testing.T) {
	reg := fscview.NewRegistry()
	ctx := &registryContext{reg: reg}
	auditA, auditB := &auditViewStub{"a"}, &auditViewStub{"b"}
	tmsA := token.TMSID{Network: "n", Channel: "c", Namespace: "a"}
	tmsB := token.TMSID{Network: "n", Channel: "c", Namespace: "b"}

	_, err := ttx.NewRegisterAuditorView(auditA, token.WithTMSID(tmsA)).Call(ctx)
	require.NoError(t, err)
	if _, err := ttx.NewRegisterAuditorView(auditB, token.WithTMSID(tmsB)).Call(ctx); err != nil {
		return // a refused second registration is acceptable
	}

	viaGet, err := reg.GetResponder(&ttx.AuditingViewInitiator{})
	require.NoError(t, err)
	viaCaller, _, _, err := reg.ResponderForCaller("", fscview.GetIdentifier(&ttx.AuditingViewInitiator{}))
	require.NoError(t, err)
	t.Logf("GetResponder is TMS B's: %v; ResponderForCaller is TMS B's: %v", viaGet == auditB, viaCaller == auditB)
	assert.Same(t, auditA, viaGet)
	assert.Same(t, auditA, viaCaller)
}
