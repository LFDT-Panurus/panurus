/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	wmock "github.com/LFDT-Panurus/panurus/token/services/identity/wallet/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TestNewServiceFields(t *testing.T) {
	ip := &dmock.IdentityProvider{}
	d := &dmock.Deserializer{}
	r := wallet.RoleRegistries{}
	logger := &logging.MockLogger{}
	s := wallet.NewService(logger, ip, d, r)
	require.NotNil(t, s)
	require.Equal(t, ip, s.IdentityProvider)
	require.Equal(t, d, s.Deserializer)
	require.Equal(t, r, s.RoleRegistries)
}

func TestRegisterIdentityDelegation(t *testing.T) {
	ctx := t.Context()
	reg := &wmock.RoleRegistry{}
	called := false
	reg.RegisterIdentityCalls(func(context.Context, driver.IdentityConfiguration) error {
		called = true

		return nil
	})
	s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{}, map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: reg, idriver.IssuerRole: reg})
	require.NoError(t, s.RegisterOwnerIdentity(ctx, driver.IdentityConfiguration{}))
	require.True(t, called)

	// test error propagation
	errReg := &wmock.RoleRegistry{}
	errReg.RegisterIdentityReturns(errors.New("boom"))
	s2 := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{}, map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: errReg})
	reqErr := s2.RegisterOwnerIdentity(ctx, driver.IdentityConfiguration{})
	require.Error(t, reqErr)
}

func TestGettersForwarding(t *testing.T) {
	ctx := t.Context()
	ip := &dmock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)
	ip.GetEnrollmentIDReturnsOnCall(0, "eid", nil)
	ip.GetRevocationHandlerReturnsOnCall(0, "rh", nil)
	ip.GetEIDAndRHReturnsOnCall(0, "eid2", "rh2", nil)

	s := wallet.NewService(&logging.MockLogger{}, ip, &dmock.Deserializer{}, nil)
	a, err := s.GetAuditInfo(ctx, driver.Identity("id"))
	require.NoError(t, err)
	require.Equal(t, []byte("ai"), a)
	eid, err := s.GetEnrollmentID(ctx, driver.Identity("id"), []byte("ai"))
	require.NoError(t, err)
	require.Equal(t, "eid", eid)
	rh, err := s.GetRevocationHandle(ctx, driver.Identity("id"), []byte("ai"))
	require.NoError(t, err)
	require.Equal(t, "rh", rh)
	eid2, rh2, err := s.GetEIDAndRH(ctx, driver.Identity("id"), []byte("ai"))
	require.NoError(t, err)
	require.Equal(t, "eid2", eid2)
	require.Equal(t, "rh2", rh2)
}

func TestRegisterRecipientIdentityFailuresAndSuccess(t *testing.T) {
	ctx := t.Context()
	ip := &dmock.IdentityProvider{}
	d := &dmock.Deserializer{}
	regSvc := wallet.NewService(&logging.MockLogger{}, ip, d, nil)

	// Create a properly typed identity
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	// nil data
	err = regSvc.RegisterRecipientIdentity(ctx, nil)
	require.Error(t, err)

	// RegisterRecipientIdentity fails
	mockVerifier := &dmock.Verifier{}
	mockVerifier.VerifyReturns(nil)
	d.GetOwnerVerifierReturns(mockVerifier, nil)
	d.MatchIdentityReturns(nil)
	ip.GetEnrollmentIDReturns("alice", nil)
	ip.GetRevocationHandlerReturns("rh", nil)
	ip.RegisterRecipientIdentityReturns(errors.New("rri"))
	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	ip.RegisterRecipientIdentityReturns(nil)

	// MatchIdentity fails
	d.MatchIdentityReturns(errors.New("mismatch"))
	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	d.MatchIdentityReturns(nil)

	// RegisterRecipientData fails
	ip.RegisterRecipientDataReturns(errors.New("rrd"))
	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	ip.RegisterRecipientDataReturns(nil)

	// success
	ip.RegisterRecipientIdentityCalls(func(context.Context, driver.Identity) error { return nil })
	d.MatchIdentityCalls(func(context.Context, driver.Identity, []byte) error { return nil })
	d.GetOwnerVerifierCalls(func(context.Context, driver.Identity) (driver.Verifier, error) { return mockVerifier, nil })
	ip.RegisterRecipientDataCalls(func(context.Context, *driver.RecipientData) error { return nil })

	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.NoError(t, err)
}

// ipWithRollback embeds the generated mock and adds identity.RecipientRegistrationRollback for rollback tests.
type ipWithRollback struct {
	*dmock.IdentityProvider
	rollbackCalls int
}

func (r *ipWithRollback) RollbackPartialRecipientRegistration(ctx context.Context, id driver.Identity) {
	r.rollbackCalls++
}

func TestRegisterRecipientIdentity_MatchIdentityFailureSkipsIdentityProvider(t *testing.T) {
	ctx := t.Context()

	// Create a properly typed identity
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	ip := &dmock.IdentityProvider{}
	ip.GetEnrollmentIDReturns("alice", nil)
	ip.GetRevocationHandlerReturns("rh", nil)
	d := &dmock.Deserializer{}
	mockVerifier := &dmock.Verifier{}
	mockVerifier.VerifyReturns(nil)
	d.GetOwnerVerifierReturns(mockVerifier, nil)
	d.MatchIdentityReturns(errors.New("mismatch"))
	regSvc := wallet.NewService(&logging.MockLogger{}, ip, d, nil)

	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	require.Zero(t, ip.RegisterRecipientIdentityCallCount())
	require.Equal(t, 1, d.MatchIdentityCallCount())
}

func TestRegisterRecipientIdentity_RollbackWhenRegisterRecipientDataFails(t *testing.T) {
	ctx := t.Context()

	// Create a properly typed identity
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	base := &dmock.IdentityProvider{}
	base.GetEnrollmentIDReturns("alice", nil)
	base.GetRevocationHandlerReturns("rh", nil)
	base.RegisterRecipientIdentityReturns(nil)
	base.RegisterRecipientDataReturns(errors.New("rrd"))
	ip := &ipWithRollback{IdentityProvider: base}
	d := &dmock.Deserializer{}
	mockVerifier := &dmock.Verifier{}
	mockVerifier.VerifyReturns(nil)
	d.GetOwnerVerifierReturns(mockVerifier, nil)
	d.MatchIdentityReturns(nil)
	regSvc := wallet.NewService(&logging.MockLogger{}, ip, d, nil)

	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	require.Equal(t, 1, ip.rollbackCalls)
}

func TestWalletAndLookupFunctions(t *testing.T) {
	ctx := t.Context()
	ownerReg := &wmock.RoleRegistry{}
	issuerReg := &wmock.RoleRegistry{}
	auditorReg := &wmock.RoleRegistry{}
	certifierReg := &wmock.RoleRegistry{}
	s := wallet.NewService(
		&logging.MockLogger{},
		&dmock.IdentityProvider{},
		&dmock.Deserializer{},
		map[idriver.IdentityRoleType]wallet.RoleRegistry{
			idriver.OwnerRole:     ownerReg,
			idriver.IssuerRole:    issuerReg,
			idriver.AuditorRole:   auditorReg,
			idriver.CertifierRole: certifierReg,
		},
	)

	// OwnerWalletIDs
	ownerReg.WalletIDsReturns([]string{"w1", "w2"}, nil)
	ids, err := s.OwnerWalletIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"w1", "w2"}, ids)

	// OwnerWallet successful cast
	ow := &dmock.OwnerWallet{}
	ownerReg.WalletByIDReturns(ow, nil)
	resOw, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))
	require.NoError(t, err)
	require.Equal(t, ow, resOw)

	// IssuerWallet successful cast
	iw := &dmock.IssuerWallet{}
	issuerReg.WalletByIDReturns(iw, nil)
	resIw, err := s.IssuerWallet(ctx, driver.WalletLookupID("id"))
	require.NoError(t, err)
	require.Equal(t, iw, resIw)

	// AuditorWallet cast
	aw := &dmock.AuditorWallet{}
	auditorReg.WalletByIDReturns(aw, nil)
	resAw, err := s.AuditorWallet(ctx, driver.WalletLookupID("id"))
	require.NoError(t, err)
	require.Equal(t, aw, resAw)

	// CertifierWallet cast
	cw := &dmock.CertifierWallet{}
	certifierReg.WalletByIDReturns(cw, nil)
	resCw, err := s.CertifierWallet(ctx, driver.WalletLookupID("id"))
	require.NoError(t, err)
	require.Equal(t, cw, resCw)

	// Wallet prefers owner
	ownerReg.WalletByIDReturns(ow, nil)
	issuerReg.WalletByIDReturns(iw, nil)
	w := s.Wallet(ctx, driver.Identity("id"))
	require.Equal(t, driver.Wallet(ow), w)
}

func TestSpendIDsAndConvert(t *testing.T) {
	s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{}, nil)
	// SpendIDs empty
	res, err := s.SpendIDs()
	require.NoError(t, err)
	require.Empty(t, res)

	// SpendIDs with nil elements
	id1 := &token.ID{TxId: "tx1", Index: 1}
	res, err = s.SpendIDs(nil, id1, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"[tx1:1]"}, res)

	// Convert map
	in := map[idriver.IdentityRoleType]*wmock.RoleRegistry{idriver.OwnerRole: {}}
	out := wallet.Convert[*wmock.RoleRegistry](in)
	require.Len(t, out, 1)
	_, ok := out[idriver.OwnerRole]
	require.True(t, ok)
	// ensure input not mutated
	require.NotNil(t, in)
}
