/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// intermediateChain returns n intermediates, each issued by the previous one, the first by root.
func intermediateChain(t *testing.T, root *TestCertificate, n int) []*TestCertificate {
	t.Helper()
	chain := make([]*TestCertificate, 0, n)
	parent := root
	for i := range n {
		c := NewTestCertificate(t, NewTestCATemplate(fmt.Sprintf("int-%d", i)), parent)
		chain = append(chain, c)
		parent = c
	}

	return chain
}

func pems(certs ...*TestCertificate) [][]byte {
	res := make([][]byte, 0, len(certs))
	for _, c := range certs {
		res = append(res, c.PEM)
	}

	return res
}

func TestNewOwnerCAs_Accepted(t *testing.T) {
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	root2 := NewTestCertificate(t, NewTestCATemplate("root2"), nil)
	chain := intermediateChain(t, root, maxChainIntermediates+1)

	noKeyUsage := NewTestCATemplate("no-key-usage")
	noKeyUsage.KeyUsage = 0
	pathLenOne := NewTestCATemplate("pathlen-one")
	pathLenOne.MaxPathLen = 1
	plRoot := NewTestCertificate(t, pathLenOne, nil)
	plInt := NewTestCertificate(t, NewTestCATemplate("pl-int"), plRoot)

	tests := []struct {
		name          string
		roots         [][]byte
		intermediates [][]byte
	}{
		{name: "empty"},
		{name: "roots only", roots: pems(root, root2)},
		{name: "root and intermediate", roots: pems(root), intermediates: pems(chain[0])},
		{name: "intermediates out of order", roots: pems(root2, root), intermediates: pems(chain[2], chain[0], chain[1])},
		{name: "max chain length", roots: pems(root), intermediates: pems(chain...)},
		{name: "ca without key usage", roots: pems(NewTestCertificate(t, noKeyUsage, nil))},
		{name: "path length honoured", roots: pems(plRoot), intermediates: pems(plInt)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOwnerCAs(tc.roots, tc.intermediates)
			require.NoError(t, err)
		})
	}
}

func TestNewOwnerCAs_Rejected(t *testing.T) {
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	foreign := NewTestCertificate(t, NewTestCATemplate("foreign"), nil)
	intermediate := NewTestCertificate(t, NewTestCATemplate("int"), root)
	foreignInt := NewTestCertificate(t, NewTestCATemplate("foreign-int"), foreign)
	tooLong := intermediateChain(t, root, maxChainIntermediates+2)

	leafTemplate := NewTestCATemplate("leaf")
	leafTemplate.IsCA = false
	leafTemplate.KeyUsage = x509.KeyUsageDigitalSignature
	leaf := NewTestCertificate(t, leafTemplate, root)

	noBasic := NewTestCATemplate("no-basic")
	noBasic.BasicConstraintsValid = false
	noBasic.IsCA = false

	noCertSign := NewTestCATemplate("no-certsign")
	noCertSign.KeyUsage = x509.KeyUsageDigitalSignature

	named := NewTestCATemplate("named")
	named.PermittedDNSDomains = []string{"example.com"}

	pathLenZero := NewTestCATemplate("pathlen-zero")
	pathLenZero.MaxPathLenZero = true
	plRoot := NewTestCertificate(t, pathLenZero, nil)
	plInt := NewTestCertificate(t, NewTestCATemplate("pl-int"), plRoot)

	pathLenOne := NewTestCATemplate("pathlen-one")
	pathLenOne.MaxPathLen = 1
	pl1Root := NewTestCertificate(t, pathLenOne, nil)
	pl1Chain := intermediateChain(t, pl1Root, 2)

	selfSignedInt := NewTestCertificate(t, NewTestCATemplate("self"), nil)

	block, _ := pem.Decode(root.PEM)
	withHeaders := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"k": "v"}, Bytes: block.Bytes})
	wrongType := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
	badDER := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})

	many := make([][]byte, maxOwnerCACerts+1)
	for i := range many {
		many[i] = root.PEM
	}

	tests := []struct {
		name          string
		roots         [][]byte
		intermediates [][]byte
		err           string
	}{
		{name: "intermediates without roots", intermediates: pems(intermediate), err: "intermediates set without roots"},
		{name: "non PEM", roots: [][]byte{[]byte("not a pem")}, err: "expected exactly one PEM block"},
		{name: "empty entry", roots: [][]byte{{}}, err: "expected exactly one PEM block"},
		{name: "wrong block type", roots: [][]byte{wrongType}, err: "unexpected PEM block type"},
		{name: "PEM headers", roots: [][]byte{withHeaders}, err: "unexpected PEM headers"},
		{name: "invalid DER", roots: [][]byte{badDER}, err: "failed to parse certificate"},
		{name: "multiple blocks", roots: [][]byte{append(bytes.Clone(root.PEM), foreign.PEM...)}, err: "expected exactly one PEM block"},
		{name: "leading bytes", roots: [][]byte{append([]byte("junk\n"), root.PEM...)}, err: "expected exactly one PEM block"},
		{name: "trailing bytes", roots: [][]byte{append(bytes.Clone(root.PEM), 'x')}, err: "trailing bytes"},
		{name: "too large", roots: [][]byte{make([]byte, maxOwnerCACertSize+1)}, err: "certificate too large"},
		{name: "too many roots", roots: many, err: "too many x509 owner root certificates"},
		{name: "too many intermediates", roots: pems(root), intermediates: many, err: "too many x509 owner intermediate certificates"},
		{name: "leaf as root", roots: pems(leaf), err: "is not a CA"},
		{name: "leaf as intermediate", roots: pems(root), intermediates: pems(leaf), err: "is not a CA"},
		{name: "no basic constraints", roots: pems(NewTestCertificate(t, noBasic, nil)), err: "is not a CA"},
		{name: "no CertSign", roots: pems(NewTestCertificate(t, noCertSign, nil)), err: "lacks the CertSign key usage"},
		{name: "name constraints", roots: pems(NewTestCertificate(t, named, nil)), err: "has name constraints"},
		{name: "intermediate not chaining", roots: pems(root), intermediates: pems(foreignInt), err: "intermediate [0] does not chain to a root"},
		{name: "self-signed intermediate", roots: pems(root), intermediates: pems(selfSignedInt), err: "does not chain to a root"},
		{name: "duplicate root", roots: pems(root, root), err: "duplicate x509 owner CA certificate"},
		{name: "root as intermediate", roots: pems(root), intermediates: pems(root), err: "duplicate x509 owner CA certificate"},
		{name: "duplicate intermediate", roots: pems(root), intermediates: pems(intermediate, intermediate), err: "duplicate x509 owner CA certificate"},
		{name: "path length zero", roots: pems(plRoot), intermediates: pems(plInt), err: "does not chain to a root"},
		{name: "path length one exceeded", roots: pems(pl1Root), intermediates: pems(pl1Chain...), err: "intermediate [1] does not chain to a root"},
		{name: "chain too long", roots: pems(root), intermediates: pems(tooLong...), err: fmt.Sprintf("intermediate [%d] does not chain to a root", maxChainIntermediates+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOwnerCAs(tc.roots, tc.intermediates)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestChainsToRoot_DeadEnd(t *testing.T) {
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	foreign := NewTestCertificate(t, NewTestCATemplate("foreign"), nil)
	good := NewTestCertificate(t, NewTestCATemplate("int"), root)

	// deadEnd has the subject and key of good but is issued by a foreign CA.
	tmpl := NewTestCATemplate("int")
	tmpl.SerialNumber = big.NewInt(1)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, foreign.Cert, &good.Key.PublicKey, foreign.Key)
	require.NoError(t, err)
	deadEnd, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	leafTemplate := NewTestCATemplate("leaf")
	leafTemplate.IsCA = false
	leafTemplate.KeyUsage = x509.KeyUsageDigitalSignature
	leaf := NewTestCertificate(t, leafTemplate, good)

	require.True(t, chainsToRoot(leaf.Cert, []*x509.Certificate{deadEnd, good.Cert}, []*x509.Certificate{root.Cert}))
	require.False(t, chainsToRoot(leaf.Cert, []*x509.Certificate{deadEnd}, []*x509.Certificate{root.Cert}))
}

// signedCert creates a certificate from template for pub, signed by parent with parentKey.
func signedCert(t *testing.T, template *x509.Certificate, pub any, parent *x509.Certificate, parentKey any) *x509.Certificate {
	t.Helper()
	tmpl := *template
	tmpl.SerialNumber = big.NewInt(int64(len(template.Subject.CommonName)) + 1)
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, parent, pub, parentKey)
	require.NoError(t, err)
	c, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return c
}

func TestParseOwnerCertificate(t *testing.T) {
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	leaf := NewTestCertificate(t, NewTestLeafTemplate("alice"), root)
	c, err := ParseOwnerCertificate(leaf.PEM)
	require.NoError(t, err)
	require.Equal(t, leaf.Cert.Raw, c.Raw)

	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	edLeaf := signedCert(t, NewTestLeafTemplate("ed"), edPub, root.Cert, root.Key)
	edPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edLeaf.Raw})
	block, _ := pem.Decode(leaf.PEM)

	tests := []struct {
		name string
		raw  []byte
		err  string
	}{
		{name: "empty", raw: nil, err: "expected exactly one PEM block"},
		{name: "multiple blocks", raw: append(bytes.Clone(leaf.PEM), root.PEM...), err: "expected exactly one PEM block"},
		{name: "leading bytes", raw: append([]byte("junk\n"), leaf.PEM...), err: "expected exactly one PEM block"},
		{name: "malformed block before", raw: append([]byte("-----BEGIN CERTIFICATE-----\n!!!!\n-----END CERTIFICATE-----\n"), leaf.PEM...), err: "expected exactly one PEM block"},
		{name: "trailing bytes", raw: append(bytes.Clone(leaf.PEM), '\n'), err: "trailing bytes"},
		{name: "public key block", raw: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes}), err: "unexpected PEM block type"},
		{name: "PEM headers", raw: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"k": "v"}, Bytes: block.Bytes}), err: "unexpected PEM headers"},
		{name: "non ECDSA key", raw: edPEM, err: "unsupported x509 owner public key type"},
		{name: "too large", raw: make([]byte, maxOwnerCACertSize+1), err: "certificate too large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseOwnerCertificate(tc.raw)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestNewOwnerCAs(t *testing.T) {
	cas, err := NewOwnerCAs(nil, nil)
	require.NoError(t, err)
	require.Nil(t, cas)

	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	_, err = NewOwnerCAs(nil, pems(root))
	require.ErrorContains(t, err, "intermediates set without roots")
	_, err = NewOwnerCAs([][]byte{[]byte("garbage")}, nil)
	require.ErrorContains(t, err, "expected exactly one PEM block")
}

func TestOwnerCAs_VerifyOwner(t *testing.T) {
	expired := func(tmpl *x509.Certificate) *x509.Certificate {
		tmpl.NotBefore = time.Now().Add(-2 * time.Hour)
		tmpl.NotAfter = time.Now().Add(-time.Hour)

		return tmpl
	}
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	intermediate := NewTestCertificate(t, NewTestCATemplate("int"), root)
	expiredRoot := NewTestCertificate(t, expired(NewTestCATemplate("expired-root")), nil)
	expiredInt := NewTestCertificate(t, expired(NewTestCATemplate("expired-int")), root)
	foreignRoot := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	pathLenZero := NewTestCATemplate("pathlen-zero")
	pathLenZero.MaxPathLenZero = true
	plRoot := NewTestCertificate(t, pathLenZero, nil)
	chain := intermediateChain(t, root, maxChainIntermediates+1)

	cas, err := NewOwnerCAs(pems(root, expiredRoot, plRoot), pems(append([]*TestCertificate{intermediate, expiredInt}, chain...)...))
	require.NoError(t, err)

	accepted := map[string]*TestCertificate{
		"root to leaf":                       NewTestCertificate(t, NewTestLeafTemplate("alice"), root),
		"root to intermediate to leaf":       NewTestCertificate(t, NewTestLeafTemplate("alice"), intermediate),
		"expired leaf":                       NewTestCertificate(t, expired(NewTestLeafTemplate("alice")), root),
		"expired intermediate":               NewTestCertificate(t, NewTestLeafTemplate("alice"), expiredInt),
		"expired root":                       NewTestCertificate(t, NewTestLeafTemplate("alice"), expiredRoot),
		"path length zero, no intermediates": NewTestCertificate(t, NewTestLeafTemplate("alice"), plRoot),
		"max chain length":                   NewTestCertificate(t, NewTestLeafTemplate("alice"), chain[maxChainIntermediates-1]),
	}
	for name, leaf := range accepted {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, cas.VerifyOwner(leaf.Cert))
		})
	}

	// plInt is not among the intermediates of cas: build a dedicated set to test MaxPathLen on leaves.
	plInt := NewTestCertificate(t, NewTestCATemplate("pl-int"), plRoot)
	plCAs := &OwnerCAs{roots: []*x509.Certificate{plRoot.Cert}, intermediates: []*x509.Certificate{plInt.Cert}}
	require.ErrorContains(t, plCAs.VerifyOwner(NewTestCertificate(t, NewTestLeafTemplate("alice"), plInt).Cert), "does not chain")

	rejected := map[string]struct {
		cert *x509.Certificate
		err  string
	}{
		"self-signed victim":                 {NewTestCertificate(t, NewTestLeafTemplate("victim"), nil).Cert, "does not chain"},
		"foreign CA with the same issuer DN": {NewTestCertificate(t, NewTestLeafTemplate("victim"), foreignRoot).Cert, "does not chain"},
		"root as leaf":                       {root.Cert, "is a CA"},
		"intermediate as leaf":               {intermediate.Cert, "is a CA"},
		"chain too long":                     {NewTestCertificate(t, NewTestLeafTemplate("alice"), chain[maxChainIntermediates]).Cert, "does not chain"},
	}
	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, cas.VerifyOwner(tc.cert), tc.err)
		})
	}
}

// TestOwnerCAs_VerifyOwner_Loop checks that a cross-signed loop of intermediates terminates.
func TestOwnerCAs_VerifyOwner_Loop(t *testing.T) {
	keyA := NewTestCertificate(t, NewTestCATemplate("a"), nil)
	keyB := NewTestCertificate(t, NewTestCATemplate("b"), nil)
	// a is issued by b and b by a: a loop with no root.
	a := signedCert(t, NewTestCATemplate("a"), &keyA.Key.PublicKey, keyB.Cert, keyB.Key)
	b := signedCert(t, NewTestCATemplate("b"), &keyB.Key.PublicKey, keyA.Cert, keyA.Key)
	root := NewTestCertificate(t, NewTestCATemplate("root"), nil)
	leaf := NewTestCertificate(t, NewTestLeafTemplate("alice"), keyA)

	cas := &OwnerCAs{roots: []*x509.Certificate{root.Cert}, intermediates: []*x509.Certificate{a, b}}
	require.ErrorContains(t, cas.VerifyOwner(leaf.Cert), "does not chain")
}
