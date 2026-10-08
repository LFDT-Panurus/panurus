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

	// A typed identity, long enough to pass validateBasicStructure's length bounds
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	// nil data
	err = regSvc.RegisterRecipientIdentity(ctx, nil)
	require.Error(t, err)

	// RegisterRecipientIdentity fails
	d.MatchIdentityReturns(nil)
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

	// A typed identity, long enough to pass validateBasicStructure's length bounds
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	ip := &dmock.IdentityProvider{}
	d := &dmock.Deserializer{}
	d.MatchIdentityReturns(errors.New("mismatch"))
	regSvc := wallet.NewService(&logging.MockLogger{}, ip, d, nil)

	err = regSvc.RegisterRecipientIdentity(ctx, &driver.RecipientData{Identity: typedID, AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`)})
	require.Error(t, err)
	require.Zero(t, ip.RegisterRecipientIdentityCallCount())
	require.Equal(t, 1, d.MatchIdentityCallCount())
}

func TestRegisterRecipientIdentity_RollbackWhenRegisterRecipientDataFails(t *testing.T) {
	ctx := t.Context()

	// A typed identity, long enough to pass validateBasicStructure's length bounds
	typedID, err := identity.WrapWithType(driver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	base := &dmock.IdentityProvider{}
	base.RegisterRecipientIdentityReturns(nil)
	base.RegisterRecipientDataReturns(errors.New("rrd"))
	ip := &ipWithRollback{IdentityProvider: base}
	d := &dmock.Deserializer{}
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

func TestWalletAccessorsMissingRegistry(t *testing.T) {
	ctx := t.Context()
	// Empty RoleRegistries: every accessor and delegator must return an error rather than
	// panicking on a nil-interface method call.
	s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{}, wallet.RoleRegistries{})

	_, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))
	require.ErrorContains(t, err, "no registry configured for owner role")

	_, err = s.IssuerWallet(ctx, driver.WalletLookupID("id"))
	require.ErrorContains(t, err, "no registry configured for issuer role")

	_, err = s.AuditorWallet(ctx, driver.WalletLookupID("id"))
	require.ErrorContains(t, err, "no registry configured for auditor role")

	_, err = s.CertifierWallet(ctx, driver.WalletLookupID("id"))
	require.ErrorContains(t, err, "no registry configured for certifier role")

	_, err = s.OwnerWalletIDs(ctx)
	require.ErrorContains(t, err, "no registry configured for owner role")

	err = s.RegisterOwnerIdentity(ctx, driver.IdentityConfiguration{})
	require.ErrorContains(t, err, "no registry configured for owner role")

	err = s.RegisterIssuerIdentity(ctx, driver.IdentityConfiguration{})
	require.ErrorContains(t, err, "no registry configured for issuer role")
}

func TestWalletAccessorsWrongWalletType(t *testing.T) {
	ctx := t.Context()
	ownerReg := &wmock.RoleRegistry{}
	// Return a wallet that implements driver.Wallet but not the expected role interface.
	// A dmock.IssuerWallet does not satisfy driver.OwnerWallet.
	notAnOwner := &dmock.IssuerWallet{}
	notAnOwner.IDReturns("w-mismatch")
	ownerReg.WalletByIDReturns(notAnOwner, nil)
	s := wallet.NewService(
		&logging.MockLogger{},
		&dmock.IdentityProvider{},
		&dmock.Deserializer{},
		map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: ownerReg},
	)

	_, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))
	require.ErrorContains(t, err, "does not implement the expected wallet interface")
	require.ErrorContains(t, err, "owner role")
	// The message reports the concrete type (%T), not the wallet id — so it must not consult ID().
	require.ErrorContains(t, err, "IssuerWallet")
	require.NotContains(t, err.Error(), "w-mismatch")
}

// TestWalletAccessorsTypedNilWalletCorrectTypeNoPanic covers the harder typed-nil shape:
// a RoleRegistry returns a non-nil driver.Wallet interface wrapping a nil pointer whose
// concrete type *does* satisfy the expected role interface (e.g. a nil *dmock.OwnerWallet
// for OwnerRole). The type assertion then succeeds, so a bare `rw, ok := w.(W)` guard lets
// the typed-nil through and the caller's first method call (wallet.ID()) panics. The
// accessor must instead detect the nil wallet and return a typed error. See #2068.
func TestWalletAccessorsTypedNilWalletCorrectTypeNoPanic(t *testing.T) {
	ctx := t.Context()
	ownerReg := &wmock.RoleRegistry{}
	var nilOwner *dmock.OwnerWallet // typed-nil that *does* satisfy driver.OwnerWallet
	ownerReg.WalletByIDReturns(nilOwner, nil)
	s := wallet.NewService(
		&logging.MockLogger{},
		&dmock.IdentityProvider{},
		&dmock.Deserializer{},
		map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: ownerReg},
	)

	require.NotPanics(t, func() {
		w, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))
		require.ErrorContains(t, err, "returned a nil wallet")
		require.Nil(t, w)
	})

	// Service.Wallet probes OwnerWallet first; a typed-nil owner wallet must not be
	// propagated out as a non-nil driver.Wallet (which would panic the eventual caller).
	require.NotPanics(t, func() {
		require.Nil(t, s.Wallet(ctx, driver.Identity("id")))
	})
}

// TestWalletAccessorsTypedNilWalletNoPanic covers the typed-nil wallet case: a RoleRegistry
// that returns a non-nil driver.Wallet interface wrapping a nil pointer which does not satisfy
// the expected role interface. The accessor must return the typed error without dereferencing
// the wallet (previously it called w.ID() and panicked on the nil receiver).
func TestWalletAccessorsTypedNilWalletNoPanic(t *testing.T) {
	ctx := t.Context()
	ownerReg := &wmock.RoleRegistry{}
	var nilWallet *dmock.IssuerWallet // typed-nil driver.Wallet, not an OwnerWallet
	ownerReg.WalletByIDReturns(nilWallet, nil)
	s := wallet.NewService(
		&logging.MockLogger{},
		&dmock.IdentityProvider{},
		&dmock.Deserializer{},
		map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: ownerReg},
	)

	require.NotPanics(t, func() {
		_, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))
		require.ErrorContains(t, err, "does not implement the expected wallet interface")
		require.ErrorContains(t, err, "owner role")
	})
}

// TestWalletAccessorsRegistryGuard table-drives all four role accessors against both
// a missing registry entry and a present-but-typed-nil entry (a non-nil RoleRegistry
// interface wrapping a nil *wmock.RoleRegistry, as Convert would produce from a nil
// concrete value). Both must yield the typed error rather than panicking.
func TestWalletAccessorsRegistryGuard(t *testing.T) {
	ctx := t.Context()

	cases := []struct {
		role string
		key  idriver.IdentityRoleType
		call func(context.Context, *wallet.Service) error
	}{
		{"owner", idriver.OwnerRole, func(ctx context.Context, s *wallet.Service) error {
			_, err := s.OwnerWallet(ctx, driver.WalletLookupID("id"))

			return err
		}},
		{"issuer", idriver.IssuerRole, func(ctx context.Context, s *wallet.Service) error {
			_, err := s.IssuerWallet(ctx, driver.WalletLookupID("id"))

			return err
		}},
		{"auditor", idriver.AuditorRole, func(ctx context.Context, s *wallet.Service) error {
			_, err := s.AuditorWallet(ctx, driver.WalletLookupID("id"))

			return err
		}},
		{"certifier", idriver.CertifierRole, func(ctx context.Context, s *wallet.Service) error {
			_, err := s.CertifierWallet(ctx, driver.WalletLookupID("id"))

			return err
		}},
	}

	for _, tc := range cases {
		t.Run("missing/"+tc.role, func(t *testing.T) {
			s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{}, wallet.RoleRegistries{})
			require.NotPanics(t, func() {
				require.ErrorContains(t, tc.call(ctx, s), "no registry configured for "+tc.role+" role")
			})
		})
		t.Run("typed-nil/"+tc.role, func(t *testing.T) {
			var nilReg *wmock.RoleRegistry // non-nil interface wrapping a nil pointer
			s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{},
				map[idriver.IdentityRoleType]wallet.RoleRegistry{tc.key: nilReg})
			require.NotPanics(t, func() {
				require.ErrorContains(t, tc.call(ctx, s), "no registry configured for "+tc.role+" role")
			})
		})
	}
}

// TestDoneSkipsNilRegistry ensures Done tolerates a typed-nil registry entry rather than
// panicking at shutdown when invoking Done() on it.
func TestDoneSkipsNilRegistry(t *testing.T) {
	liveReg := &wmock.RoleRegistry{}
	liveReg.DoneReturns(nil)
	var nilReg *wmock.RoleRegistry // non-nil interface wrapping a nil pointer
	s := wallet.NewService(&logging.MockLogger{}, &dmock.IdentityProvider{}, &dmock.Deserializer{},
		map[idriver.IdentityRoleType]wallet.RoleRegistry{idriver.OwnerRole: liveReg, idriver.IssuerRole: nilReg})

	require.NotPanics(t, func() {
		require.NoError(t, s.Done())
	})
	require.Equal(t, 1, liveReg.DoneCallCount())
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
