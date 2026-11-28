/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzOwnerCertificatesNoPanic fuzzes the x509 owner CA validation, the strict owner certificate parser
// and the chain walk. The CA lists come from the public parameters, the owner certificate from peers.
func FuzzOwnerCertificatesNoPanic(f *testing.F) {
	root := NewTestCertificate(f, NewTestCATemplate("root"), nil)
	intermediate := NewTestCertificate(f, NewTestCATemplate("int"), root)
	leaf := NewTestCertificate(f, NewTestLeafTemplate("alice"), intermediate)
	forged := NewTestCertificate(f, NewTestLeafTemplate("alice"), nil)
	cas, err := NewOwnerCAs([][]byte{root.PEM}, [][]byte{intermediate.PEM})
	require.NoError(f, err)

	f.Add(root.PEM, intermediate.PEM, leaf.PEM)
	f.Add(root.PEM, intermediate.PEM, forged.PEM)
	f.Add([]byte{}, []byte{}, []byte{})
	f.Add(root.PEM[:len(root.PEM)/2], intermediate.PEM[:10], leaf.PEM[:len(leaf.PEM)-5])
	f.Add(append(bytes.Clone(root.PEM), intermediate.PEM...), []byte("garbage"), append(bytes.Clone(leaf.PEM), 'x'))
	f.Add(leaf.PEM, root.PEM, root.PEM)

	f.Fuzz(func(t *testing.T, rootRaw, intermediateRaw, leafRaw []byte) {
		require.NotPanics(t, func() {
			_, _ = NewOwnerCAs([][]byte{rootRaw}, [][]byte{intermediateRaw})
			_, _ = NewOwnerCAs([][]byte{root.PEM}, [][]byte{intermediateRaw})
			if fuzzed, err := NewOwnerCAs([][]byte{rootRaw}, nil); err == nil && fuzzed != nil {
				_ = fuzzed.VerifyOwner(leaf.Cert)
			}
			cert, err := ParseOwnerCertificate(leafRaw)
			if err != nil {
				return
			}
			_ = cas.VerifyOwner(cert)
			_ = EnrollmentIDFromCert(cert)
			_, _ = RevocationHandleFromCert(cert)
		})
	})
}
