/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prepared_test

import (
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
)

// ── Shared setup (SetupAll is expensive; run once for all tests here) ───────

var (
	setupOnce sync.Once
	sharedPP  *pp.PublicParams
	setupErr  error
)

func setupSharedPP(t *testing.T) *pp.PublicParams {
	t.Helper()
	setupOnce.Do(func() {
		p := pp.DefaultPublicParams()
		var err error
		p, err = setup.SetupAll(p)
		if err != nil {
			setupErr = errors.Wrapf(err, "shared setup.SetupAll")

			return
		}
		sharedPP = p
	})
	if setupErr != nil {
		t.Fatalf("shared setup failed: %v", setupErr)
	}

	return sharedPP
}

// ── NewParams ────────────────────────────────────────────────────────────────

func TestNewParams(t *testing.T) {
	p := setupSharedPP(t)

	base, err := prepared.NewParams(p)
	require.NoError(t, err)
	require.NotNil(t, base)
	require.Same(t, p, base.PP())
	require.NotNil(t, base.Backend())
	require.NotNil(t, base.SpendCS())
	require.NotNil(t, base.OutputCS())
	require.Nil(t, base.MigrationCS(), "migration circuit is only compiled via WithMigration")
}

func TestNewParams_UnsupportedProofSystem(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "spartan"

	_, err := prepared.NewParams(p)
	require.Error(t, err)
}

// ── Verifier ─────────────────────────────────────────────────────────────────

func TestNewVerifier(t *testing.T) {
	p := setupSharedPP(t)

	v, err := prepared.NewVerifier(p)
	require.NoError(t, err)
	require.NotNil(t, v.VKSpend())
	require.NotNil(t, v.VKOutput())
	require.Nil(t, v.VKMigration(), "default public params carry no migration key")
}

func TestNewVerifierFrom_SharesParams(t *testing.T) {
	p := setupSharedPP(t)

	base, err := prepared.NewParams(p)
	require.NoError(t, err)

	v, err := prepared.NewVerifierFrom(base)
	require.NoError(t, err)
	require.Same(t, base, v.Params)
}

func TestNewVerifier_MissingSpendKey(t *testing.T) {
	p := setupSharedPP(t)
	withoutSpendVK := *p
	withoutSpendVK.VKSpend = nil

	_, err := prepared.NewVerifier(&withoutSpendVK)
	require.Error(t, err)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestNewVerifier_MalformedSpendKey(t *testing.T) {
	p := setupSharedPP(t)
	corrupt := *p
	corrupt.VKSpend = []byte("not-a-valid-verifying-key")

	_, err := prepared.NewVerifier(&corrupt)
	require.Error(t, err, "a present but malformed spend verifying key must be a hard error")
}

func TestVerifier_WithMigration_MissingKeyIsError(t *testing.T) {
	p := setupSharedPP(t)

	v, err := prepared.NewVerifier(p)
	require.NoError(t, err)
	require.Nil(t, v.VKMigration())

	_, err = v.WithMigration(setup.PedersenGeneratorCoords{})
	require.Error(t, err, "WithMigration must fail when no migration verifying key is configured")
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

// ── Prover ───────────────────────────────────────────────────────────────────

func TestNewProver(t *testing.T) {
	p := setupSharedPP(t)

	pr, err := prepared.NewProver(p)
	require.NoError(t, err)
	require.NotNil(t, pr.PKSpend())
	require.NotNil(t, pr.PKOutput())
	require.Nil(t, pr.PKMigration(), "default public params carry no migration key")
}

func TestNewProverFrom_SharesParams(t *testing.T) {
	p := setupSharedPP(t)

	base, err := prepared.NewParams(p)
	require.NoError(t, err)

	pr, err := prepared.NewProverFrom(base)
	require.NoError(t, err)
	require.Same(t, base, pr.Params)
}

func TestNewProver_MissingSpendKey(t *testing.T) {
	p := setupSharedPP(t)
	withoutSpendPK := *p
	withoutSpendPK.PKSpend = nil

	_, err := prepared.NewProver(&withoutSpendPK)
	require.Error(t, err)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestNewProver_MalformedOutputKey(t *testing.T) {
	p := setupSharedPP(t)
	corrupt := *p
	corrupt.PKOutput = []byte("not-a-valid-proving-key")

	_, err := prepared.NewProver(&corrupt)
	require.Error(t, err, "a present but malformed output proving key must be a hard error")
}

func TestProver_WithMigration_MissingKeyIsError(t *testing.T) {
	p := setupSharedPP(t)

	pr, err := prepared.NewProver(p)
	require.NoError(t, err)
	require.Nil(t, pr.PKMigration())

	_, err = pr.WithMigration(setup.PedersenGeneratorCoords{})
	require.Error(t, err, "WithMigration must fail when no migration proving key is configured")
	require.ErrorIs(t, err, setup.ErrMissingKey)
}
