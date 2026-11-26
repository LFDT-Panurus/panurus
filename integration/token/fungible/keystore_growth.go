/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fungible

import (
	"context"
	"encoding/json"
	"time"

	tplatform "github.com/LFDT-Panurus/panurus/integration/nwo/token"
	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/onsi/gomega"
)

const (
	keystoreGrowthTimeout     = 30 * time.Second
	keystoreGrowthPolling     = 200 * time.Millisecond
	keystoreGrowthConsistency = 3 * time.Second
)

// KeystoreGrowth is the key store growth of a node since a baseline.
type KeystoreGrowth struct {
	// Keys is the growth of the key store.
	Keys int
	// SignerSKIs is the growth of the distinct SKIs of the Signers rows.
	SignerSKIs int
	// SignerSKIsInKeystore is the growth of the Signers SKIs that have a key.
	SignerSKIsInKeystore int
	// SignerSKIsWithToken is the growth of the Signers SKIs whose identity owns, or owned, a token.
	SignerSKIsWithToken int
	// UnresolvedSigners is the growth of the Signers rows the stats view could not resolve to an SKI.
	UnresolvedSigners int
}

// expectedKeystoreGrowth is the key store growth model of one idemix owner wallet with cache size n.
// registered tells whether the wallet is registered, runs is the number of node processes that used
// the wallet, and used is the number of pseudonyms the wallet put in token outputs.
//
// Each process run fills one lazy cache of n entries, holding n+1 pseudonyms, and every
// pseudonym key is stored when it is generated. With n <= 0 no cache is filled.
// role.RecipientDataCache entries have a Signers row, so they are visible to skicleanup.
//
// Change this model, and only this model, to benchmark a change in key consumption.
func expectedKeystoreGrowth(n int, registered bool, runs int, used int) KeystoreGrowth {
	usk := expectedUSK(registered)
	cached := 0
	if n > 0 {
		cached = (n + 1) * runs
	}

	return KeystoreGrowth{
		Keys:                 usk + used + cached,
		SignerSKIs:           used + cached,
		SignerSKIsInKeystore: used + cached,
		SignerSKIsWithToken:  used,
	}
}

// TestKeystoreGrowth registers an owner wallet at runtime on the given node, funds it, and spends it
// across a restart of the node. After each step, it checks the key store growth of the node against
// expectedKeystoreGrowth, and logs the measured statistics. The node's owner cache size must be
// cacheSize and its storage cleanup must be disabled, see common.Opts.KeystoreGrowthCacheSize.
func TestKeystoreGrowth(network *integration.Infrastructure, auditorID string, cacheSize int, sel *token3.ReplicaSelector, onRestart OnRestartFunc) {
	const (
		walletID  = common2.KeystoreGrowthNode + ".w1"
		tokenType = "KSG"
	)
	ctx := context.Background()
	auditor := sel.Get(auditorID)
	issuer := sel.Get("issuer")
	node := sel.Get(common2.KeystoreGrowthNode)
	bob := sel.Get("bob")

	RegisterAuditor(network, auditor)

	baseline := GetKeystoreStats(network, node)
	logger.Infof("keystore growth [baseline]: %+v", baseline)
	registered, runs, used := false, 0, 0
	check := func(step string) {
		expected := expectedKeystoreGrowth(cacheSize, registered, runs, used)
		growth := func() (KeystoreGrowth, error) {
			stats, err := getKeystoreStats(network, node)
			if err != nil {
				return KeystoreGrowth{}, err
			}

			return keystoreGrowth(baseline, stats), nil
		}
		gomega.Eventually(growth).WithTimeout(keystoreGrowthTimeout).WithPolling(keystoreGrowthPolling).
			Should(gomega.Equal(expected), "step [%s]", step)
		gomega.Consistently(growth).WithTimeout(keystoreGrowthConsistency).WithPolling(keystoreGrowthPolling).
			Should(gomega.Equal(expected), "step [%s]", step)
		stats := GetKeystoreStats(network, node)
		g := keystoreGrowth(baseline, stats)
		logger.Infof(
			"keystore growth [%s]: keys [%d], signer SKIs [%d], with token [%d], visible orphans [%d], invisible orphans [%d], stats %+v",
			step, g.Keys, g.SignerSKIs, g.SignerSKIsWithToken,
			g.SignerSKIsInKeystore-g.SignerSKIsWithToken, g.Keys-expectedUSK(registered)-g.SignerSKIsInKeystore, stats,
		)
	}
	transfer := func(step string) {
		TransferCash(network, node, walletID, tokenType, 10, bob, auditor)
		used++ // the change output
		check(step)
	}

	tokenPlatform := tplatform.GetPlatform(network.NWOCtx, "token")
	idConfig := tokenPlatform.GenOwnerCryptoMaterial(tokenPlatform.GetTopology().TMSs[0].BackendTopology.Name(), node.Id(), walletID, false)
	RegisterOwnerIdentity(ctx, network, node, idConfig)
	registered = true
	check("register")

	issueCashToWallet(network, tokenType, 100, node, walletID, auditor, issuer)
	runs, used = 1, 1
	check("issue")

	transfer("transfer 1")
	transfer("transfer 2")

	Restart(network, false, onRestart, node)
	check("restart")

	runs++
	transfer("transfer 3")
	transfer("transfer 4")

	CheckBalance(network, node, walletID, tokenType, 60)
}

// issueCashToWallet issues to a runtime-registered wallet of node: such a wallet has no FSC endpoint
// of its own, so the issuer contacts node and names the wallet.
func issueCashToWallet(network *integration.Infrastructure, typ token.Type, amount uint64, node *token3.NodeReference, walletID string, auditor, issuer *token3.NodeReference) {
	txIDBoxed, err := network.Client(issuer.ReplicaName()).CallView("issue", common.JSONMarshall(&views.IssueCash{
		Anonymous:         true,
		Auditor:           auditor.Id(),
		TokenType:         typ,
		Quantity:          amount,
		Recipient:         network.Identity(node.Id()),
		RecipientWalletID: walletID,
		RecipientEID:      walletID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txIDBoxed)
	for _, n := range []*token3.NodeReference{node, auditor, issuer} {
		common2.CheckFinality(network, n, txID, nil, false)
	}
}

// GetKeystoreStats returns the key store statistics of the given node.
func GetKeystoreStats(network *integration.Infrastructure, ref *token3.NodeReference) *views.KeystoreStats {
	stats, err := getKeystoreStats(network, ref)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return stats
}

func getKeystoreStats(network *integration.Infrastructure, ref *token3.NodeReference) (*views.KeystoreStats, error) {
	res, err := network.Client(ref.ReplicaName()).CallView("KeystoreStats", common.JSONMarshall(&views.KeystoreStatsQuery{}))
	if err != nil {
		return nil, err
	}
	raw, ok := res.([]byte)
	if !ok {
		return nil, errors.Errorf("unexpected KeystoreStats result type [%T]", res)
	}
	stats := &views.KeystoreStats{}
	if err := json.Unmarshal(raw, stats); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling KeystoreStats")
	}

	return stats, nil
}

func keystoreGrowth(baseline, stats *views.KeystoreStats) KeystoreGrowth {
	return KeystoreGrowth{
		Keys:                 stats.Count - baseline.Count,
		SignerSKIs:           stats.DistinctSignerSKIs - baseline.DistinctSignerSKIs,
		SignerSKIsInKeystore: stats.SignerSKIsInKeystore - baseline.SignerSKIsInKeystore,
		SignerSKIsWithToken:  stats.SignerSKIsWithToken - baseline.SignerSKIsWithToken,
		UnresolvedSigners:    stats.UnresolvedSigners - baseline.UnresolvedSigners,
	}
}

func expectedUSK(registered bool) int {
	if registered {
		return 1
	}

	return 0
}
