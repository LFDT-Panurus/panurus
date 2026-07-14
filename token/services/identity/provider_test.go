/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvider_RegisterRecipientData(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	data := &driver.RecipientData{
		Identity:               driver.Identity("an_id"),
		AuditInfo:              []byte("audit"),
		TokenMetadata:          []byte("meta"),
		TokenMetadataAuditInfo: []byte("meta_audit"),
	}

	storage.StoreIdentityDataReturns(nil)

	err := p.RegisterRecipientData(t.Context(), data)
	require.NoError(t, err)

	// assert storage called once with expected args
	_, id, ai, tm, tmai := storage.StoreIdentityDataArgsForCall(0)
	assert.Equal(t, []byte("an_id"), id)
	assert.Equal(t, []byte("audit"), ai)
	assert.Equal(t, []byte("meta"), tm)
	assert.Equal(t, []byte("meta_audit"), tmai)
}

func TestProvider_RegisterSigner_And_IsMe(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	id := driver.Identity("signer_id")
	signer := &drvmock.Signer{}
	verifier := &drvmock.Verifier{}

	// storage.RegisterIdentityDescriptor should be invoked
	storage.RegisterIdentityDescriptorReturns(nil)

	err := p.RegisterSigner(t.Context(), id, signer, verifier, []byte("si"), false)
	require.NoError(t, err)

	// storage called
	require.Equal(t, 1, storage.RegisterIdentityDescriptorCallCount())

	// provider should now consider this identity as "me"
	isMe := p.IsMe(t.Context(), id)
	assert.True(t, isMe)
}

func TestProvider_GetSigner_Deserializable(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	id := driver.Identity("an_identity")
	expected := &drvmock.Signer{}

	// deserializer should return the signer
	des.DeserializeSignerReturns(expected, nil)
	storage.StoreSignerInfoReturns(nil)

	s, err := p.GetSigner(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, expected, s)

	// ensure StoreSignerInfo was invoked to persist signer info
	require.Equal(t, 1, storage.StoreSignerInfoCallCount())
}

func TestProvider_GetSigner_TypedIdentityFallback(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	// create a typed identity wrapping an inner identity
	inner := driver.Identity("inner")
	ti := identity.TypedIdentity{Type: identity.Type(5), Identity: inner}
	outerBytes, err := ti.Bytes()
	require.NoError(t, err)
	outer := driver.Identity(outerBytes)

	expected := &drvmock.Signer{}

	// Deserializer should fail for outer identity but succeed for inner
	des.DeserializeSignerReturnsOnCall(0, nil, errors.New("not deserializable"))
	des.DeserializeSignerReturnsOnCall(1, expected, nil)
	storage.StoreSignerInfoReturns(nil)

	s, err := p.GetSigner(t.Context(), outer)
	require.NoError(t, err)
	assert.Equal(t, expected, s)
	// persisted
	require.Equal(t, 2, storage.StoreSignerInfoCallCount())
}

func TestProvider_Bind(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	longTerm := driver.Identity("lt")
	e1 := driver.Identity("e1")
	e2 := driver.Identity("e2")

	nbs.BindReturns(nil)

	err := p.Bind(t.Context(), longTerm, e1, e2)
	require.NoError(t, err)

	// bind should be called twice (for e1 and e2)
	require.Equal(t, 2, nbs.BindCallCount())
}

func TestProvider_EnrollmentIDHelpers(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	id := driver.Identity("who")
	audit := []byte("audit")

	eidu.GetEnrollmentIDReturns("e-id", nil)
	v, err := p.GetEnrollmentID(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "e-id", v)

	eidu.GetRevocationHandlerReturns("rh", nil)
	rh, err := p.GetRevocationHandler(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "rh", rh)

	eidu.GetEIDAndRHReturns("e2", "rh2", nil)
	eid, erh, err := p.GetEIDAndRH(t.Context(), id, audit)
	require.NoError(t, err)
	assert.Equal(t, "e2", eid)
	assert.Equal(t, "rh2", erh)
}

func TestProvider_RollbackPartialRecipientRegistration(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	id := driver.Identity("recipient_id")
	require.NoError(t, p.RegisterRecipientIdentity(t.Context(), id))

	var rb identity.RecipientRegistrationRollback = p
	rb.RollbackPartialRecipientRegistration(t.Context(), id)
}

func TestProvider_GetAuditInfo(t *testing.T) {
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	nbs := &idmock.NetworkBinderService{}
	eidu := &idmock.EnrollmentIDUnmarshaler{}

	p := identity.NewProvider(logging.MustGetLogger(), storage, des, nbs, eidu)

	id := driver.Identity("who")
	audit := []byte("audit-data")
	storage.GetAuditInfoReturns(audit, nil)

	ai, err := p.GetAuditInfo(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, audit, ai)
	require.Equal(t, 1, storage.GetAuditInfoCallCount())
}
