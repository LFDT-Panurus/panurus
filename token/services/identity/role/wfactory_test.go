/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultFactory(t *testing.T) {
	setup := func(t *testing.T) (*role.DefaultFactory, *mock.IdentityProvider, *mock.TokenVault, *mock.WalletsConfiguration, *mock.Deserializer, *mock.IdentitySupport) {
		t.Helper()
		ip := &mock.IdentityProvider{}
		tv := &mock.TokenVault{}
		wc := &mock.WalletsConfiguration{}
		des := &mock.Deserializer{}
		is := &mock.IdentitySupport{}
		logger := logging.MustGetLogger("test")

		f := role.NewDefaultFactory(logger, ip, tv, wc, des, &disabled.Provider{})
		require.NotNil(t, f)

		return f, ip, tv, wc, des, is
	}

	t.Run("Owner Wallet - Anonymous", func(t *testing.T) {
		f, _, _, wc, _, is := setup(t)
		id := "owner-wallet-anon"
		info := &mockIdentityInfo{id: id, anonymous: true}

		wc.CacheSizeForOwnerIDReturns(5)
		is.ContainsIdentityReturns(true) // For NewAnonymousOwnerWallet check

		w, err := f.NewWallet(t.Context(), id, idriver.OwnerRole, is, info)
		require.NoError(t, err)
		require.NotNil(t, w)
		assert.Equal(t, id, w.ID())
		// Is AnonymousOwnerWallet? Hard to check type directly without export, but behavior confirms it.
	})

	t.Run("Owner Wallet - LongTerm", func(t *testing.T) {
		f, _, _, _, _, reg := setup(t)
		id := "owner-wallet-lt"
		info := &mockIdentityInfo{id: id, anonymous: false}

		w, err := f.NewWallet(t.Context(), id, idriver.OwnerRole, reg, info)
		require.NoError(t, err)
		require.NotNil(t, w)
		assert.Equal(t, id, w.ID())
	})

	t.Run("Issuer Wallet", func(t *testing.T) {
		f, ip, _, _, _, reg := setup(t)
		id := "issuer-wallet"
		info := &mockIdentityInfo{id: "issuer-id", anonymous: false}

		signer := &mock.Signer{}
		ip.GetSignerReturns(signer, nil)

		w, err := f.NewWallet(t.Context(), id, idriver.IssuerRole, reg, info)
		require.NoError(t, err)
		require.NotNil(t, w)
		assert.Equal(t, id, w.ID())

		// Verify bindings
		assert.Equal(t, 1, reg.BindIdentityCallCount())
		_, _, eid, wid, _ := reg.BindIdentityArgsForCall(0)
		assert.Equal(t, info.EnrollmentID(), eid)
		assert.Equal(t, id, wid)
	})

	t.Run("Issuer Wallet - Failures", func(t *testing.T) {
		f, ip, _, _, _, reg := setup(t)
		id := "issuer-wallet-fail"

		// Case 1: Info Get fails
		infoErr := &mockIdentityInfo{err: errors.New("info error")}
		_, err := f.NewWallet(t.Context(), id, idriver.IssuerRole, reg, infoErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get issuer wallet identity")

		// Case 2: GetSigner fails
		info := &mockIdentityInfo{id: "issuer-id"}
		ip.GetSignerReturns(nil, errors.New("signer error"))
		_, err = f.NewWallet(t.Context(), id, idriver.IssuerRole, reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get issuer signer")

		// Case 3: BindIdentity fails
		ip.GetSignerReturns(&mock.Signer{}, nil)
		reg.BindIdentityReturns(errors.New("bind error"))
		_, err = f.NewWallet(t.Context(), id, idriver.IssuerRole, reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to register recipient identity")
	})

	t.Run("Auditor Wallet", func(t *testing.T) {
		f, ip, _, _, _, reg := setup(t)
		id := "auditor-wallet"
		info := &mockIdentityInfo{id: "auditor-id"}

		signer := &mock.Signer{}
		ip.GetSignerReturns(signer, nil)

		w, err := f.NewWallet(t.Context(), id, idriver.AuditorRole, reg, info)
		require.NoError(t, err)
		require.NotNil(t, w)
		assert.Equal(t, id, w.ID())

		// Verify bindings
		assert.Equal(t, 1, reg.BindIdentityCallCount())
	})

	t.Run("Auditor Wallet - Failures", func(t *testing.T) {
		f, ip, _, _, _, reg := setup(t)
		id := "auditor-wallet-fail"

		// Case 1: Info Get fails
		infoErr := &mockIdentityInfo{err: errors.New("info error")}
		_, err := f.NewWallet(t.Context(), id, idriver.AuditorRole, reg, infoErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get auditor wallet identity")

		// Case 2: GetSigner fails
		info := &mockIdentityInfo{id: "auditor-id"}
		ip.GetSignerReturns(nil, errors.New("signer error"))
		_, err = f.NewWallet(t.Context(), id, idriver.AuditorRole, reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get auditor signer")

		// Case 3: BindIdentity fails
		ip.GetSignerReturns(&mock.Signer{}, nil)
		reg.BindIdentityReturns(errors.New("bind error"))
		_, err = f.NewWallet(t.Context(), id, idriver.AuditorRole, reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to register recipient identity")
	})

	t.Run("Certifier Wallet - Unsupported", func(t *testing.T) {
		f, _, _, _, _, reg := setup(t)
		id := "certifier-wallet"
		info := &mockIdentityInfo{id: "cert-id"}

		_, err := f.NewWallet(t.Context(), id, idriver.CertifierRole, reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "certifiers are not supported")
	})

	t.Run("Unknown Role - Unsupported", func(t *testing.T) {
		f, _, _, _, _, reg := setup(t)
		id := "unknown-wallet"
		info := &mockIdentityInfo{id: "unk-id"}

		_, err := f.NewWallet(t.Context(), id, idriver.IdentityRoleType(999), reg, info)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "role [999] not supported")
	})
}

// mockIdentityInfo is a helper to mock identity.Info
type mockIdentityInfo struct {
	id        string
	eid       string
	anonymous bool
	remote    bool
	err       error
}

func (f *mockIdentityInfo) ID() string {
	return f.id
}

func (f *mockIdentityInfo) EnrollmentID() string {
	if f.eid == "" {
		return "enrollment-id"
	}

	return f.eid
}

func (f *mockIdentityInfo) Type() string {
	return "msp"
}

func (f *mockIdentityInfo) Get(context.Context) (driver.Identity, []byte, error) {
	if f.err != nil {
		return nil, nil, f.err
	}

	return driver.Identity(f.id), []byte("audit-info"), nil
}

func (f *mockIdentityInfo) Anonymous() bool {
	return f.anonymous
}

func (f *mockIdentityInfo) Remote() bool {
	return f.remote
}
