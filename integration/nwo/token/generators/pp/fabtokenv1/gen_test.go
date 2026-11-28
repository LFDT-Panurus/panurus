/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	cryptogen "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/stretchr/testify/require"
)

// tokenDirPlatform is a generators.TokenPlatform that only serves TokenDir.
type tokenDirPlatform struct {
	generators.TokenPlatform
	dir string
}

func (p *tokenDirPlatform) TokenDir() string { return p.dir }

func TestGenerateOwnerRoots(t *testing.T) {
	tms := &topology.TMS{Network: "fabric", Channel: "testchannel", Namespace: "token"}
	root, err := cryptogen.NewCryptoMaterialGenerator(&tokenDirPlatform{dir: t.TempDir()}, nil).Setup(tms)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(filepath.Join(root, cryptogen.OwnerCACertFileName))
	require.NoError(t, err)

	generate := func(args ...any) *setup.PublicParams {
		t.Helper()
		raw, err := NewFabTokenPublicParamsGenerator(DefaultDriverVersion).Generate(tms, &topology.Wallets{}, args...)
		require.NoError(t, err)
		pp, err := setup.NewPublicParamsFromBytes(raw, setup.FabTokenDriverName, DefaultDriverVersion)
		require.NoError(t, err)
		require.NoError(t, pp.Validate())

		return pp
	}

	pp := generate(root, "32")
	require.Equal(t, [][]byte{caPEM}, pp.X509OwnerRoots)
	require.Empty(t, pp.X509OwnerIntermediates)
	require.Equal(t, uint64(32), pp.Precision())

	// no owner CA directory, or a directory without the CA: no roots
	require.Empty(t, generate("", "32").X509OwnerRoots)
	require.Empty(t, generate(t.TempDir()).X509OwnerRoots)
	require.Empty(t, generate().X509OwnerRoots)
}
