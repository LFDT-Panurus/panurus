/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	idrivermock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warnLogger records the Warnf messages.
type warnLogger struct {
	logging.MockLogger
	warnings []string
}

func (l *warnLogger) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestWarnUntrustedOwners(t *testing.T) {
	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("owner-root"), nil)
	owner := func(cert *crypto.TestCertificate) (tdriver.Identity, []byte) {
		id, err := identity.WrapWithType(x509.IdentityType, cert.PEM)
		require.NoError(t, err)
		rh, err := crypto.RevocationHandleFromCert(cert.Cert)
		require.NoError(t, err)
		info, err := (&x509.AuditInfo{EID: "alice", RH: rh}).Bytes()
		require.NoError(t, err)

		return id, info
	}
	legitID, legitInfo := owner(crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("alice"), root))
	forgedID, forgedInfo := owner(crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("alice"), nil))

	eidrh, err := NewEIDRHDeserializer(&setup.PublicParams{X509OwnerRoots: [][]byte{root.PEM}})
	require.NoError(t, err)
	role := &idrivermock.Role{}
	role.IdentityIDsReturns([]string{"legit", "forged", "missing"}, nil)
	role.GetIdentityInfoStub = func(_ context.Context, id string) (idriver.IdentityInfo, error) {
		get := func(context.Context) (tdriver.Identity, []byte, error) {
			switch id {
			case "legit":
				return legitID, legitInfo, nil
			case "forged":
				return forgedID, forgedInfo, nil
			}

			return nil, nil, errors.New("not found")
		}

		return membership.NewIdentityInfo(&membership.LocalIdentity{Name: id}, get), nil
	}

	logger := &warnLogger{}
	warnUntrustedOwners(t.Context(), logger, role, eidrh)
	require.Len(t, logger.warnings, 2)
	assert.Contains(t, logger.warnings[0], "local owner identity [forged] fails the x509 owner checks")
	assert.Contains(t, logger.warnings[0], "does not chain")
	assert.Contains(t, logger.warnings[1], "failed to get local owner identity [missing]")

	role.IdentityIDsReturns(nil, errors.New("boom"))
	logger = &warnLogger{}
	warnUntrustedOwners(t.Context(), logger, role, eidrh)
	require.Len(t, logger.warnings, 1)
	assert.Contains(t, logger.warnings[0], "failed to list local owner identities")
}
