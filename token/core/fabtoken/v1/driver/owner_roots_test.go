/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	htlcid "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// x509Owner is a typed x509 owner identity, its audit info and its certificate.
type x509Owner struct {
	id   tdriver.Identity
	info []byte
	cert *crypto.TestCertificate
}

// newX509Owner returns cert as a typed x509 identity with audit info naming eid and the RH of cert.
func newX509Owner(t *testing.T, cert *crypto.TestCertificate, eid string) x509Owner {
	t.Helper()
	id, err := identity.WrapWithType(x509.IdentityType, cert.PEM)
	require.NoError(t, err)
	rh, err := crypto.RevocationHandleFromCert(cert.Cert)
	require.NoError(t, err)
	info, err := (&x509.AuditInfo{EID: eid, RH: rh}).Bytes()
	require.NoError(t, err)

	return x509Owner{id: id, info: info, cert: cert}
}

// ownerRootsFixture holds public parameters with an x509 owner root, a legit owner issued by it,
// a forged self-signed owner claiming the same enrollment ID, and a self-signed issuer listed in the PP.
type ownerRootsFixture struct {
	pp     *setup.PublicParams
	legit  x509Owner
	forged x509Owner
	issuer x509Owner
}

func newOwnerRootsFixture(t *testing.T) *ownerRootsFixture {
	t.Helper()
	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("owner-root"), nil)
	issuer := newX509Owner(t, crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("issuer"), nil), "issuer")
	pp, err := setup.NewWith(setup.FabTokenDriverName, setup.ProtocolV1, 64)
	require.NoError(t, err)
	pp.IssuerIDs = []tdriver.Identity{issuer.id}
	pp.X509OwnerRoots = [][]byte{root.PEM}
	require.NoError(t, pp.Validate())

	return &ownerRootsFixture{
		pp:     pp,
		legit:  newX509Owner(t, crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), root), "victim"),
		forged: newX509Owner(t, crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), nil), "victim"),
		issuer: issuer,
	}
}

func (f *ownerRootsFixture) deserializers(t *testing.T, pp *setup.PublicParams) (*driver.Deserializer, *driver.EIDRHDeserializer) {
	t.Helper()
	des, err := driver.NewDeserializer(pp)
	require.NoError(t, err)
	eidrh, err := driver.NewEIDRHDeserializer(pp)
	require.NoError(t, err)

	return des, eidrh
}

// wrapped returns member wrapped in multisig, boolpolicy and htlc identities, with audit info.
func wrapped(t *testing.T, member x509Owner) map[string]x509Owner {
	t.Helper()
	ms, err := multisig.WrapIdentities(member.id)
	require.NoError(t, err)
	msInfo, err := multisig.WrapAuditInfo([][]byte{member.info})
	require.NoError(t, err)
	bp, err := boolpolicy.WrapPolicyIdentity("$0", member.id)
	require.NoError(t, err)
	bpInfo, err := boolpolicy.WrapAuditInfo([][]byte{member.info})
	require.NoError(t, err)
	script, err := json.Marshal(&htlc.Script{Sender: member.id, Recipient: member.id})
	require.NoError(t, err)
	hs, err := identity.WrapWithType(htlc.ScriptType, script)
	require.NoError(t, err)
	hsInfo, err := (&htlcid.ScriptInfo{Sender: member.info, Recipient: member.info}).Marshal()
	require.NoError(t, err)

	return map[string]x509Owner{
		"multisig":   {id: ms, info: msInfo},
		"boolpolicy": {id: bp, info: bpInfo},
		"htlc":       {id: hs, info: hsInfo},
	}
}

// TestOwnerRoots_ForgedOwnerRejected is the exploit regression of #2868: with owner roots in the PP,
// a self-signed CN=victim certificate with self-consistent audit info no longer passes as victim.
func TestOwnerRoots_ForgedOwnerRejected(t *testing.T) {
	f := newOwnerRootsFixture(t)
	des, eidrh := f.deserializers(t, f.pp)
	ctx := t.Context()

	cases := wrapped(t, f.forged)
	cases["x509"] = f.forged
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, des.MatchIdentity(ctx, o.id, o.info), identity.ErrUntrustedIdentity)

			_, err := eidrh.GetEnrollmentID(ctx, o.id, o.info)
			require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
			require.NotErrorIs(t, err, identity.ErrUnresolvableIdentity)
		})
	}

	// the forged identity cannot register as a recipient
	ip := &mock.IdentityProvider{}
	ws := wallet.NewService(logging.MustGetLogger(), ip, des, nil)
	err := ws.RegisterRecipientIdentity(ctx, &tdriver.RecipientData{Identity: f.forged.id, AuditInfo: f.forged.info})
	require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
	assert.Zero(t, ip.RegisterRecipientIdentityCallCount())
	require.NoError(t, ws.RegisterRecipientIdentity(ctx, &tdriver.RecipientData{Identity: f.legit.id, AuditInfo: f.legit.info}))
	assert.Equal(t, 1, ip.RegisterRecipientIdentityCallCount())
}

// TestOwnerRoots_ForgedOwnerMalformedAuditInfo checks that malformed audit info cannot turn an untrusted
// owner into an unresolvable one, which the auditor service would skip instead of failing.
func TestOwnerRoots_ForgedOwnerMalformedAuditInfo(t *testing.T) {
	f := newOwnerRootsFixture(t)
	_, eidrh := f.deserializers(t, f.pp)
	ctx := t.Context()

	for name, info := range map[string][]byte{"wrong type": []byte(`{"EID":5}`), "not json": []byte("garbage")} {
		t.Run(name, func(t *testing.T) {
			_, err := eidrh.GetEnrollmentID(ctx, f.forged.id, info)
			require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
			require.NotErrorIs(t, err, identity.ErrUnresolvableIdentity)

			// a trusted owner with malformed audit info stays unresolvable, as without roots
			_, err = eidrh.GetEnrollmentID(ctx, f.legit.id, info)
			require.ErrorIs(t, err, identity.ErrUnresolvableIdentity)
			require.NotErrorIs(t, err, identity.ErrUntrustedIdentity)
		})
	}
}

func TestOwnerRoots_Accepted(t *testing.T) {
	f := newOwnerRootsFixture(t)
	des, eidrh := f.deserializers(t, f.pp)
	ctx := t.Context()

	cases := wrapped(t, f.legit)
	cases["x509"] = f.legit
	// the issuer listed in the PP does not chain to the owner roots but is exempt
	cases["listed issuer"] = f.issuer
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, des.MatchIdentity(ctx, o.id, o.info))
			_, err := eidrh.GetEnrollmentID(ctx, o.id, o.info)
			require.NoError(t, err)
		})
	}
	eid, err := eidrh.GetEnrollmentID(ctx, f.legit.id, f.legit.info)
	require.NoError(t, err)
	assert.Equal(t, "victim", eid)
	eid, err = eidrh.GetEnrollmentID(ctx, f.issuer.id, f.issuer.info)
	require.NoError(t, err)
	assert.Equal(t, "issuer", eid)
}

// TestOwnerRoots_Legacy pins that a PP without owner roots keeps accepting the forged owner.
func TestOwnerRoots_Legacy(t *testing.T) {
	f := newOwnerRootsFixture(t)
	pp := *f.pp
	pp.X509OwnerRoots = nil
	des, eidrh := f.deserializers(t, &pp)
	ctx := t.Context()

	require.NoError(t, des.MatchIdentity(ctx, f.forged.id, f.forged.info))
	eid, err := eidrh.GetEnrollmentID(ctx, f.forged.id, f.forged.info)
	require.NoError(t, err)
	assert.Equal(t, "victim", eid)
}

// TestOwnerRoots_VerifierUnchanged pins that owner roots do not change signature verification:
// the forged owner still gets a verifier, and the fabtoken validator accepts its signature on an input.
func TestOwnerRoots_VerifierUnchanged(t *testing.T) {
	f := newOwnerRootsFixture(t)
	des, _ := f.deserializers(t, f.pp)
	ctx := t.Context()

	v, err := des.GetOwnerVerifier(ctx, f.forged.id)
	require.NoError(t, err)
	msg := []byte("message")
	sig, err := crypto.NewEcdsaSigner(f.forged.cert.Key).Sign(msg)
	require.NoError(t, err)
	require.NoError(t, v.Verify(msg, sig))

	sp := &mock.SignatureProvider{}
	sp.HasBeenSignedByStub = func(_ context.Context, _ tdriver.Identity, v tdriver.Verifier) ([]byte, error) {
		return sig, v.Verify(msg, sig)
	}
	c := &validator.Context{
		PP:                f.pp,
		Deserializer:      des,
		SignatureProvider: sp,
		Logger:            logging.MustGetLogger(),
		TransferAction: &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{{Input: &actions.Output{Owner: f.forged.id, Type: "USD", Quantity: "0x1"}}},
		},
	}
	require.NoError(t, validator.TransferSignatureValidate(ctx, c))
	assert.Equal(t, 1, sp.HasBeenSignedByCallCount())
}
