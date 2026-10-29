/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// upgradePPM serves fixed public parameters.
type upgradePPM struct{ pp *pp.PublicParams }

func (m *upgradePPM) PublicParams() *pp.PublicParams               { return m.pp }
func (m *upgradePPM) PublicParameters() driver.PublicParameters    { return m.pp }
func (m *upgradePPM) NewCertifierKeyPair() ([]byte, []byte, error) { return nil, nil, nil }
func (m *upgradePPM) PublicParamsHash() driver.PPHash              { return nil }

// acceptAllUpgrades accepts every tokens upgrade request.
type acceptAllUpgrades struct{}

func (acceptAllUpgrades) ProcessTokensUpgradeRequest(context.Context, *driver.TokenUpgradeRequest) ([]token.Token, error) {
	return nil, nil
}

func fabtokenUpgradeRequest(t *testing.T, quantity string) *driver.TokenUpgradeRequest {
	t.Helper()
	owner, err := identity.WrapWithType(driver.X509IdentityType, []byte("alice"))
	require.NoError(t, err)
	raw, err := (&actions.Output{Owner: owner, Type: "ABC", Quantity: quantity}).Serialize()
	require.NoError(t, err)
	format, err := fabtoken.SupportedTokenFormat(64)
	require.NoError(t, err)

	return &driver.TokenUpgradeRequest{Tokens: []token.LedgerToken{{ID: token.ID{TxId: "fab", Index: 0}, Format: format, Token: raw}}}
}

// TestIssueService_UpgradeRejections checks the argument checks of the
// burn-and-re-issue branch of IssueService.Issue, and that a total needing more
// outputs than MaxOutputs is refused before any proving: the orchestrator is
// nil, so reaching it would panic.
func TestIssueService_UpgradeRejections(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 8 // MaxTokenValue = 255
	recipient := [][]byte{[]byte("bob")}
	wallet := &mock.IssuerWallet{}
	newService := func(up zkatsnark.TokensUpgradeRequestProcessor) *zkatsnark.IssueService {
		return zkatsnark.NewIssueService(logging.MustGetLogger(), &upgradePPM{pp: p}, nil, nil, nil, up, nil, nil)
	}
	issue := func(s *zkatsnark.IssueService, owners [][]byte, opts *driver.IssueOptions) error {
		_, _, err := s.Issue(t.Context(), nil, "", nil, owners, opts)

		return err
	}

	t.Run("no tokens", func(t *testing.T) {
		require.EqualError(t, issue(newService(acceptAllUpgrades{}), recipient, &driver.IssueOptions{}), "upgrade: no tokens to upgrade")
	})
	t.Run("no recipient", func(t *testing.T) {
		opts := &driver.IssueOptions{TokensUpgradeRequest: fabtokenUpgradeRequest(t, "0x10"), Wallet: wallet}
		require.EqualError(t, issue(newService(acceptAllUpgrades{}), nil, opts), "upgrade: expected exactly one recipient, got [0]")
	})
	t.Run("two recipients", func(t *testing.T) {
		opts := &driver.IssueOptions{TokensUpgradeRequest: fabtokenUpgradeRequest(t, "0x10"), Wallet: wallet}
		require.EqualError(t, issue(newService(acceptAllUpgrades{}), [][]byte{[]byte("bob"), []byte("carol")}, opts), "upgrade: expected exactly one recipient, got [2]")
	})
	t.Run("nil upgrade service", func(t *testing.T) {
		opts := &driver.IssueOptions{TokensUpgradeRequest: fabtokenUpgradeRequest(t, "0x10"), Wallet: wallet}
		require.EqualError(t, issue(newService(nil), recipient, opts), "upgrade: tokens upgrade service not configured")
	})
	t.Run("nil wallet", func(t *testing.T) {
		opts := &driver.IssueOptions{TokensUpgradeRequest: fabtokenUpgradeRequest(t, "0x10")}
		require.EqualError(t, issue(newService(acceptAllUpgrades{}), recipient, opts), "upgrade: issuer wallet not provided")
	})
	t.Run("too many outputs", func(t *testing.T) {
		// 0xff01 = 65281 = 256*255 + 1: 257 outputs of at most 255 are needed
		opts := &driver.IssueOptions{TokensUpgradeRequest: fabtokenUpgradeRequest(t, "0xff01"), Wallet: wallet}
		maxOutputs := driver.ResourceLimits{}.WithDefaults().MaxOutputs
		require.Equal(t, 256, maxOutputs)
		require.ErrorContains(t, issue(newService(acceptAllUpgrades{}), recipient, opts), "total value [65281] needs [257] outputs of at most [255], exceeding the limit of [256] outputs")
		require.Zero(t, wallet.GetIssuerIdentityCallCount(), "refused before resolving the issuer and proving")
	})
}
