/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
)

// tokenDirPlatform is a generators.TokenPlatform that only serves TokenDir.
type tokenDirPlatform struct {
	generators.TokenPlatform
	dir string
}

func (p *tokenDirPlatform) TokenDir() string { return p.dir }

func TestOwnerCA(t *testing.T) {
	gomega.RegisterTestingT(t)
	tp := &tokenDirPlatform{dir: t.TempDir()}
	tms := &topology.TMS{Network: "fabric", Channel: "testchannel", Namespace: "token"}
	g := NewCryptoMaterialGenerator(tp, nil)

	root, err := g.Setup(tms)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tp.dir, "crypto", tms.ID(), ownerCADirName), root)
	caPEM, err := os.ReadFile(filepath.Join(root, OwnerCACertFileName))
	require.NoError(t, err)
	_, err = crypto.NewOwnerCAs([][]byte{caPEM}, nil)
	require.NoError(t, err)

	// Setup is idempotent, also across generators running concurrently
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			again, err := NewCryptoMaterialGenerator(tp, nil).Setup(tms)
			require.NoError(t, err)
			require.Equal(t, root, again)
		})
	}
	wg.Wait()
	again, err := os.ReadFile(filepath.Join(root, OwnerCACertFileName))
	require.NoError(t, err)
	require.Equal(t, caPEM, again)

	n := node.NewNode("alice")
	topology.ToOptions(n.Options).SetRemoteOwner("bob")
	ids := g.GenerateOwnerIdentities(tms, n, "alice", "bob")
	require.Len(t, ids, 2)

	checker, err := x509.NewOwnerChainChecker([][]byte{caPEM}, nil, nil)
	require.NoError(t, err)
	for _, id := range ids {
		cn := id.ID + "@Orgalice.example.com"
		require.Equal(t, filepath.Join(tp.dir, "crypto", tms.ID(), "alice", "owners", "peerOrganizations", "Orgalice.example.com", "users", cn, "msp"), id.Path)
		certPEM, err := os.ReadFile(filepath.Join(id.Path, crypto.SignCertsDirName, cn+"-cert.pem"))
		require.NoError(t, err)
		cert, err := crypto.ParseOwnerCertificate(certPEM)
		require.NoError(t, err)
		require.Equal(t, cn, cert.Subject.CommonName)

		keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
		keystore := ""
		if id.ID == "bob" {
			// remote owner: the key is only in keystoreFull
			require.NoFileExists(t, filepath.Join(id.Path, x509.KeystoreFolder, x509.PrivateKeyFileName))
			keystore = x509.KeystoreFullFolder
		}
		km, _, err := x509.NewKeyManagerFromConf(nil, id.Path, keystore, nil, keyStore)
		require.NoError(t, err)
		desc, err := km.Identity(context.Background(), nil)
		require.NoError(t, err)
		require.Equal(t, certPEM, []byte(desc.Identity))
		if id.ID == "alice" {
			msg := []byte("hello")
			sigma, err := km.SigningIdentity().Sign(msg)
			require.NoError(t, err)
			verifier, err := km.DeserializeVerifier(context.Background(), desc.Identity)
			require.NoError(t, err)
			require.NoError(t, verifier.Verify(msg, sigma))
		}

		// the owner chains to the owner CA, and its audit info is bound to its certificate
		ai, err := (&x509.AuditInfoDeserializer{Checker: checker}).DeserializeAuditInfo(context.Background(), desc.Identity, desc.AuditInfo)
		require.NoError(t, err)
		require.Equal(t, id.ID, ai.EnrollmentID())
	}

	// a CA not in the owner roots is rejected
	otherCA := filepath.Join(t.TempDir(), ownerCADirName)
	require.NoError(t, setupOwnerCA(otherCA, "other"))
	otherPEM, err := os.ReadFile(filepath.Join(otherCA, OwnerCACertFileName))
	require.NoError(t, err)
	otherChecker, err := x509.NewOwnerChainChecker([][]byte{otherPEM}, nil, nil)
	require.NoError(t, err)
	km, _, err := x509.NewKeyManager(ids[0].Path, nil, x509.NewKeyStore(kvs.NewTrackedMemory()))
	require.NoError(t, err)
	desc, err := km.Identity(context.Background(), nil)
	require.NoError(t, err)
	_, err = (&x509.AuditInfoDeserializer{Checker: otherChecker}).DeserializeAuditInfo(context.Background(), desc.Identity, desc.AuditInfo)
	require.Error(t, err)
}

func TestLoadOwnerCARejectsMismatchedKey(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	require.NoError(t, setupOwnerCA(a, "a"))
	require.NoError(t, setupOwnerCA(b, "b"))
	key, err := os.ReadFile(filepath.Join(b, ownerCAKeyFileName))
	require.NoError(t, err)
	//nolint:gosec // test temp dir
	require.NoError(t, os.WriteFile(filepath.Join(a, ownerCAKeyFileName), key, 0600))
	_, err = loadOwnerCA(a)
	require.ErrorContains(t, err, "does not match")
	require.Error(t, setupOwnerCA(a, "a"))
}
