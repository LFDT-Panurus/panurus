/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interop

import (
	"crypto"
	"fmt"
	"math/big"
	"sync"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/interop/views"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	token3 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/onsi/gomega"
)

const (
	EUR = token3.Type("EUR")
	USD = token3.Type("USD")
)

func TestHTLCSingleNetwork(network *integration.Infrastructure, sel *token2.ReplicaSelector) {
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	issuer := sel.Get("issuer")
	auditor := sel.Get("auditor")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	defaultTMSID := token.TMSID{}
	RegisterAuditor(network)

	IssueCash(network, "", USD, 110, alice, alice)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 110, 0, 0, -1)
	IssueCash(network, "", USD, 10, alice, alice)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 120, 0, 0, -1)

	h := ListIssuerHistory(network, "", USD)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(120))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType(USD).Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "", EUR)
	gomega.Expect(h.Count()).To(gomega.BeEquivalentTo(0))

	IssueCash(network, "", EUR, 10, bob, bob)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 10, 0, 0, -1)
	IssueCash(network, "", EUR, 10, bob, bob)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 20, 0, 0, -1)
	IssueCash(network, "", EUR, 10, bob, bob)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)

	h = ListIssuerHistory(network, "", USD)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(120))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType(USD).Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "", EUR)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(30))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType(EUR).Count()).To(gomega.BeEquivalentTo(h.Count()))

	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 120, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 0, 0, -1)

	Restart(network, issuer, auditor, alice, bob)
	RegisterAuditor(network)

	// htlc (lock, failing claim, reclaim)
	_, preImage, _ := HTLCLock(network, token.TMSID{}, alice, "", USD, 10, bob, auditor, 10*time.Second, nil, crypto.SHA512)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 110, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 10, 0, -1)
	time.Sleep(15 * time.Second)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 110, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 0, 10, -1)
	htlcClaim(network, defaultTMSID, bob, "", preImage, auditor, "deadline elapsed")
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 110, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 0, 10, -1)

	HTLCReclaimAll(network, alice, "")
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 120, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 0, 0, -1)

	// htlc (lock, claim)
	_, preImage, _ = HTLCLock(network, defaultTMSID, alice, "", USD, 20, bob, auditor, 1*time.Hour, nil, crypto.SHA3_256)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 100, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 0, 20, 0, -1)

	htlcClaim(network, defaultTMSID, bob, "", preImage, auditor)
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 100, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 20, 0, 0, -1)

	// payment limit reached
	_, _, _ = HTLCLock(network, defaultTMSID, alice, "", USD, uint64(views.Limit+10), bob, auditor, 1*time.Hour, nil, crypto.SHA3_256, "payment limit reached")
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 100, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 0, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 30, 0, 0, -1)
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 20, 0, 0, -1)

	CheckPublicParams(network, defaultTMSID, issuer, auditor, alice, bob)
	<-time.After(30 * time.Second)
	CheckOwnerStore(network, defaultTMSID, nil, issuer, alice, bob)
	CheckAuditorStore(network, defaultTMSID, auditor, "", nil)

	// lock two times with the same hash, the second lock should fail
	_, _, hash := HTLCLock(network, defaultTMSID, alice, "", USD, 1, bob, auditor, 1*time.Hour, nil, crypto.SHA3_256)
	_, _, _ = HTLCLock(network, defaultTMSID, alice, "", USD, 1, bob, auditor, 1*time.Hour, hash, crypto.SHA3_256,
		fmt.Sprintf(
			"entry with transfer metadata key [%s] is already occupied",
			htlc.LockKey(hash),
		),
	)
	HTLCLock(network, defaultTMSID, alice, "", USD, 1, bob, auditor, 1*time.Hour, nil, crypto.SHA3_256)

	CheckPublicParams(network, defaultTMSID, issuer, auditor, alice, bob)
	CheckOwnerStore(network, defaultTMSID, nil, issuer, auditor, alice, bob)
	CheckAuditorStore(network, defaultTMSID, auditor, "", nil)
	PruneInvalidUnspentTokens(network, defaultTMSID, issuer, auditor, alice, bob)
	for _, name := range []*token2.NodeReference{alice, bob} {
		IDs := ListVaultUnspentTokens(network, defaultTMSID, name)
		CheckIfExistsInVault(network, defaultTMSID, auditor, IDs)
	}
}

func TestHTLCTwoNetworks(network *integration.Infrastructure, sel *token2.ReplicaSelector) {
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	auditor := sel.Get("auditor")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	alpha := token.TMSID{Network: "alpha"}
	beta := token.TMSID{Network: "beta"}

	RegisterAuditor(network, token.WithTMSID(alpha))
	RegisterAuditor(network, token.WithTMSID(beta))

	IssueCashWithTMS(network, alpha, issuer, "", EUR, 30, alice, auditor)
	CheckBalanceAndHolding(network, alice, "", EUR, 30, token.WithTMSID(alpha))

	IssueCashWithTMS(network, beta, issuer, "", USD, 30, bob, auditor)
	CheckBalanceAndHolding(network, bob, "", USD, 30, token.WithTMSID(beta))

	_, preImage, hash := HTLCLock(network, alpha, alice, "", EUR, 10, bob, auditor, 1*time.Hour, nil, 0)
	HTLCLock(network, beta, bob, "", USD, 10, alice, auditor, 1*time.Hour, hash, 0)
	htlcClaim(network, beta, alice, "", preImage, auditor)
	htlcClaim(network, alpha, bob, "", preImage, auditor)

	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 20, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 10, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 10, 0, 0, -1, token.WithTMSID(beta))
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 20, 0, 0, -1, token.WithTMSID(beta))

	// Try to claim again and get an error

	htlcClaim(network, beta, alice, "", preImage, auditor, "expected only one htlc script to match")
	htlcClaim(network, alpha, bob, "", preImage, auditor, "expected only one htlc script to match")

	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 20, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 10, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 10, 0, 0, -1, token.WithTMSID(beta))
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 20, 0, 0, -1, token.WithTMSID(beta))

	// Try to claim without locking

	htlcClaim(network, beta, alice, "", nil, auditor, "expected only one htlc script to match")
	htlcClaim(network, alpha, bob, "", nil, auditor, "expected only one htlc script to match")

	CheckBalanceWithLockedAndHolding(network, alice, "", EUR, 20, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, bob, "", EUR, 10, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, alice, "", USD, 10, 0, 0, -1, token.WithTMSID(beta))
	CheckBalanceWithLockedAndHolding(network, bob, "", USD, 20, 0, 0, -1, token.WithTMSID(beta))

	CheckPublicParams(network, alpha, issuer, auditor, alice, bob)
	CheckPublicParams(network, beta, issuer, auditor, alice, bob)
	CheckOwnerStore(network, alpha, nil, issuer, auditor, alice, bob)
	CheckOwnerStore(network, beta, nil, issuer, auditor, alice, bob)
	CheckAuditorStore(network, alpha, sel.Get("auditor"), "", nil)
	CheckAuditorStore(network, beta, sel.Get("auditor"), "", nil)
	PruneInvalidUnspentTokens(network, alpha, issuer, auditor, alice, bob)
	PruneInvalidUnspentTokens(network, beta, issuer, auditor, alice, bob)
	for _, name := range []*token2.NodeReference{alice, bob} {
		aIDs := ListVaultUnspentTokens(network, alpha, name)
		CheckIfExistsInVault(network, alpha, auditor, aIDs)
		bIDs := ListVaultUnspentTokens(network, beta, name)
		CheckIfExistsInVault(network, beta, auditor, bIDs)
	}

	// "alice" locks to "alice.id1", the deadline expires, "alice" reclaims, "alice.id1" checks the existence of an expired received locked token
	_, _, h := HTLCLock(network, alpha, alice, "", EUR, 10, sel.Get("alice.id1"), auditor, 10*time.Second, nil, 0)
	time.Sleep(10 * time.Second)
	HTLCReclaimByHash(network, alpha, alice, "", h)
	HTLCCheckExistenceReceivedExpiredByHash(network, alpha, alice, "alice.id1", h, false)
}

func TestHTLCNoCrossClaimTwoNetworks(network *integration.Infrastructure, sel *token2.ReplicaSelector) {
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	auditor := sel.Get("auditor")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	alpha := token.TMSID{Network: "alpha"}
	beta := token.TMSID{Network: "beta"}

	RegisterAuditor(network, token.WithTMSID(alpha))
	RegisterAuditor(network, token.WithTMSID(beta))

	IssueCashWithTMS(network, alpha, issuer, "", EUR, 30, sel.Get("alice.id1"), auditor)
	CheckBalanceAndHolding(network, alice, "alice.id1", EUR, 30, token.WithTMSID(alpha))

	IssueCashWithTMS(network, beta, issuer, "", USD, 30, sel.Get("bob.id1"), auditor)
	CheckBalanceAndHolding(network, bob, "bob.id1", USD, 30, token.WithTMSID(beta))

	_, preImage, hash := HTLCLock(network, alpha, alice, "alice.id1", EUR, 10, sel.Get("alice.id2"), auditor, 30*time.Second, nil, 0)
	_, _, _ = HTLCLock(network, beta, bob, "bob.id1", USD, 10, sel.Get("bob.id2"), auditor, 30*time.Second, hash, 0)

	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		gomega.Expect(func() {
			htlcClaim(network, alpha, alice, "alice.id2", preImage, auditor)
		}).ToNot(gomega.Panic())
	}()
	go func() {
		defer wg.Done()
		gomega.Expect(func() {
			htlcClaim(network, beta, bob, "bob.id2", preImage, auditor)
		}).ToNot(gomega.Panic())
	}()
	scan(network, alice, hash, crypto.SHA256, token.WithTMSID(alpha))
	scan(network, alice, hash, crypto.SHA256, token.WithTMSID(alpha))

	scan(network, bob, hash, crypto.SHA256, token.WithTMSID(beta))
	scan(network, bob, hash, crypto.SHA256, token.WithTMSID(beta))

	// wait the completion of the claims above
	wg.Wait()

	CheckBalanceWithLockedAndHolding(network, alice, "alice.id1", EUR, 20, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, alice, "alice.id2", EUR, 10, 0, 0, -1, token.WithTMSID(alpha))
	CheckBalanceWithLockedAndHolding(network, bob, "bob.id1", USD, 20, 0, 0, -1, token.WithTMSID(beta))
	CheckBalanceWithLockedAndHolding(network, bob, "bob.id2", USD, 10, 0, 0, -1, token.WithTMSID(beta))

	IssueCashWithTMS(network, alpha, issuer, "", EUR, 30, sel.Get("alice.id1"), auditor)

	scan(network, bob, hash, crypto.SHA256, token.WithTMSID(beta))
	scan(network, alice, hash, crypto.SHA256, token.WithTMSID(alpha))

	CheckPublicParams(network, token.TMSID{}, alice, bob)
	CheckPublicParams(network, alpha, issuer, auditor)
	CheckPublicParams(network, beta, issuer, auditor)
	CheckOwnerStore(network, token.TMSID{}, nil, auditor, alice, bob)
	CheckOwnerStore(network, alpha, nil, issuer)
	CheckOwnerStore(network, beta, nil, issuer)
	CheckAuditorStore(network, alpha, auditor, "", nil)
	CheckAuditorStore(network, beta, auditor, "", nil)
	PruneInvalidUnspentTokens(network, alpha, issuer, auditor)
	PruneInvalidUnspentTokens(network, beta, issuer, auditor)

	PruneInvalidUnspentTokens(network, alpha, alice)
	aIDs := ListVaultUnspentTokens(network, alpha, alice)
	CheckIfExistsInVault(network, alpha, sel.Get("auditor"), aIDs)

	PruneInvalidUnspentTokens(network, beta, bob)
	bIDs := ListVaultUnspentTokens(network, beta, bob)
	CheckIfExistsInVault(network, beta, sel.Get("auditor"), bIDs)
}

func TestFastExchange(network *integration.Infrastructure, sel *token2.ReplicaSelector) {
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	auditor := sel.Get("auditor")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	alpha := token.TMSID{Network: "alpha"}
	beta := token.TMSID{Network: "beta"}

	RegisterAuditor(network, token.WithTMSID(alpha))
	RegisterAuditor(network, token.WithTMSID(beta))

	IssueCashWithTMS(network, alpha, issuer, "", EUR, 30, alice, auditor)
	CheckBalance(network, sel.Get("alice"), "", EUR, 30, token.WithTMSID(alpha))

	IssueCashWithTMS(network, beta, issuer, "", USD, 30, bob, auditor)
	CheckBalance(network, bob, "", USD, 30, token.WithTMSID(beta))

	fastExchange(network, alice, bob, alpha, EUR, 10, beta, USD, 10, 1*time.Hour)

	CheckBalance(network, sel.Get("alice"), "", EUR, 20, token.WithTMSID(alpha))

	gomega.Eventually(CheckBalanceReturnError).WithArguments(network, sel.Get("bob"), "", EUR, uint64(10), token.WithTMSID(alpha)).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(gomega.Succeed())

	CheckBalance(network, sel.Get("alice"), "", USD, 10, token.WithTMSID(beta))
	gomega.Eventually(CheckBalanceReturnError).WithArguments(network, sel.Get("bob"), "", USD, uint64(20), token.WithTMSID(beta)).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(gomega.Succeed())
}
