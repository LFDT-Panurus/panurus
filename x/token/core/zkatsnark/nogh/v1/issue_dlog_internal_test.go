/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
)

type staticPP struct {
	p *pp.PublicParams
}

func (s staticPP) PublicParams() *pp.PublicParams               { return s.p }
func (s staticPP) PublicParameters() driver.PublicParameters    { return s.p }
func (s staticPP) NewCertifierKeyPair() ([]byte, []byte, error) { return nil, nil, nil }
func (s staticPP) PublicParamsHash() driver.PPHash              { return nil }

// TestOpenDLogInputs checks the refusals of the issuer, before any proving,
// on the ZKAT-DLOG tokens of an upgrade request.
func TestOpenDLogInputs(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 8
	legacy, err := dlogpp.Embed(p, dlogpp.DefaultCurve)
	require.NoError(t, err)
	ld, err := p.LegacyDLog()
	require.NoError(t, err)
	owner, err := identity.WrapWithType(driver.X509IdentityType, []byte("alice"))
	require.NoError(t, err)
	tok := func(i uint64, typ string, value uint64) token2.LedgerToken {
		m, err := dlogpp.Mint(legacy, owner, typ, value)
		require.NoError(t, err)

		return token2.LedgerToken{ID: token2.ID{TxId: "tx", Index: i}, Token: m.Raw, TokenMetadata: m.Metadata}
	}

	inputs, typ, total, err := openDLogInputs([]token2.LedgerToken{tok(0, "USD", 200), tok(1, "USD", 100)}, ld, p)
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	require.Equal(t, token2.Type("USD"), typ)
	require.Equal(t, uint64(300), total)

	script, err := identity.WrapWithType(driver.HTLCScriptIdentityType, []byte("script"))
	require.NoError(t, err)
	m, err := dlogpp.Mint(legacy, script, "USD", 1)
	require.NoError(t, err)
	scriptTok := token2.LedgerToken{ID: token2.ID{TxId: "tx", Index: 9}, Token: m.Raw, TokenMetadata: m.Metadata}

	for name, tokens := range map[string][]token2.LedgerToken{
		"none":                {},
		"mixed types":         {tok(0, "USD", 1), tok(1, "EUR", 1)},
		"zero value":          {tok(0, "USD", 0)},
		"above MaxTokenValue": {tok(0, "USD", 256)},
		"duplicate":           {tok(0, "USD", 1), tok(0, "USD", 1)},
		"non-plain owner":     {scriptTok},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := openDLogInputs(tokens, ld, p)
			require.Error(t, err)
		})
	}
}

// TestUpgradeDLog_NotConfigured checks that the issuer refuses ZKAT-DLOG
// upgrades without a migration prover or ZKAT-DLOG public parameters.
func TestUpgradeDLog_NotConfigured(t *testing.T) {
	p := pp.DefaultPublicParams()
	opts := &driver.IssueOptions{TokensUpgradeRequest: &driver.TokenUpgradeRequest{Tokens: []token2.LedgerToken{{}}}}
	s := &IssueService{Logger: logging.MustGetLogger(), PublicParametersManager: staticPP{p}, Orchestrator: prover.NewOrchestrator(nil, nil)}
	_, _, err := s.upgradeDLog(t.Context(), []byte("bob"), opts)
	require.ErrorContains(t, err, "no migration prover")
	s.Orchestrator = nil
	_, _, err = s.upgradeDLog(t.Context(), []byte("bob"), opts)
	require.ErrorContains(t, err, "no migration prover")
}
