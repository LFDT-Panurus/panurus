/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fungible

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/runner/nwo"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	dlog "github.com/LFDT-Panurus/panurus/integration/token/fungible/dlogstress/support"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	token4 "github.com/LFDT-Panurus/panurus/token"
	dlognoghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fsclogging "github.com/hyperledger-labs/fabric-smart-client/platform/common/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/onsi/gomega"
)

const (
	// DLogNamespace is the default name for the namespace where the token chaincode is deployed when using the dlog driver
	// #nosec G101 no passwords here
	DLogNamespace = "dlog_token_chaincode"
	// FabTokenNamespace is the default name for the namespace where the token chaincode is deployed when using the fabtoken driver
	// #nosec G101  no passwords here
	FabTokenNamespace = "fabtoken_token_chaincode"

	// transferTimeout is the maximum time to wait for a transfer operation to complete in tests.
	// This generous timeout accounts for variable CI environment performance.
	transferTimeout = 60 * time.Second
)

var AuditedTransactions = []TransactionRecord{
	{
		TransactionRecord: ttxdb.TransactionRecord{
			TxID:         "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "USD",
			Amount:       big.NewInt(110),
			Status:       ttxdb.Confirmed,
		},
	},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "USD",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "USD",
			Amount:       big.NewInt(111),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "alice",
			TokenType:    "USD",
			Amount:       big.NewInt(9),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "bob",
			RecipientEID: "bob",
			TokenType:    "USD",
			Amount:       big.NewInt(100),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Redeem,
			SenderEID:    "bob",
			RecipientEID: "",
			TokenType:    "USD",
			Amount:       big.NewInt(11),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "USD",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "LIRA",
			Amount:       big.NewInt(3),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "LIRA",
			Amount:       big.NewInt(3),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "LIRA",
			Amount:       big.NewInt(2),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "alice",
			TokenType:    "LIRA",
			Amount:       big.NewInt(1),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "charlie",
			TokenType:    "LIRA",
			Amount:       big.NewInt(3),
			Status:       ttxdb.Confirmed,
		}},
}

var AliceAcceptedTransactions = []TransactionRecord{
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "USD",
			Amount:       big.NewInt(110),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "USD",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
}

var AliceID1AcceptedTransactions = []TransactionRecord{
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "alice",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
}

var BobAcceptedTransactions = []TransactionRecord{
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "EUR",
			Amount:       big.NewInt(10),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "USD",
			Amount:       big.NewInt(111),
			Status:       ttxdb.Confirmed,
		}},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Transfer,
			SenderEID:    "bob",
			RecipientEID: "bob",
			TokenType:    "USD",
			Amount:       big.NewInt(100),
			Status:       ttxdb.Confirmed,
		},
		CheckNext: true,
	},
	{
		TransactionRecord: ttxdb.TransactionRecord{TxID: "",
			ActionType:   ttxdb.Redeem,
			SenderEID:    "bob",
			RecipientEID: "",
			TokenType:    "USD",
			Amount:       big.NewInt(11),
			Status:       ttxdb.Confirmed,
		},
		CheckPrevious: true,
	},
}

type OnRestartFunc = func(*integration.Infrastructure, string)

func TestAll(network *integration.Infrastructure, auditorId string, onRestart OnRestartFunc, aries bool, sel *token3.ReplicaSelector) {
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	endorsers := GetEndorsers(network, sel)
	custodian := sel.Get("custodian")
	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	t0 := time.Now()
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	IssueSuccessfulCash(network, "", "USD", 110, alice, auditor, true, issuer, endorsers...)
	t1 := time.Now()
	CheckBalanceAndHolding(network, alice, "", "USD", 110, auditor)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:1], nil, nil)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:1], &t0, &t1)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:1], nil, nil, nil, ttxdb.Issue)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:1], nil, nil, []ttxdb.TxStatus{ttxdb.Confirmed}, ttxdb.Issue)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:1], nil, nil, nil)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:1], &t0, &t1, nil)

	t2 := time.Now()
	Withdraw(network, nil, false, alice, "", "USD", 10, auditor, issuer)
	t3 := time.Now()
	CheckBalanceAndHolding(network, alice, "", "USD", 120, auditor)
	CheckBalanceAndHolding(network, alice, "alice", "USD", 120, auditor)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:2], nil, nil)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:2], &t0, &t3)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[1:2], &t2, &t3)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:2], nil, nil, nil)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:2], &t0, &t3, nil)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[1:2], &t2, &t3, nil)

	h := ListIssuerHistory(network, "", "USD", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(120))).To(gomega.BeEquivalentTo(0), "expected [%d]=[120]", h.Sum(64).ToBigInt().Int64())
	gomega.Expect(h.ByType("USD").Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "", "EUR", issuer)
	gomega.Expect(h.Count()).To(gomega.BeEquivalentTo(0))

	Restart(network, true, onRestart, auditor)
	RegisterAuditor(network, auditor)

	CheckBalanceAndHolding(network, alice, "", "USD", 120, auditor)
	CheckBalanceAndHolding(network, alice, "alice", "USD", 120, auditor)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:2], nil, nil)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:2], &t0, &t3)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[1:2], &t2, &t3)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:2], nil, nil, nil)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[:2], &t0, &t3, nil)
	CheckAcceptedTransactions(network, alice, "", AliceAcceptedTransactions[1:2], &t2, &t3, nil)

	h = ListIssuerHistory(network, "", "USD", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(120))).To(gomega.BeEquivalentTo(0), "expected [%d]=[120]", h.Sum(64).ToBigInt().Int64())
	gomega.Expect(h.ByType("USD").Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "", "EUR", issuer)
	gomega.Expect(h.Count()).To(gomega.BeEquivalentTo(0))

	// Register a new issuer wallet and issue with that wallet
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)
	tokenPlatform := token.GetPlatform(network.NWOCtx, "token")
	gomega.Expect(tokenPlatform).ToNot(gomega.BeNil(), "cannot find token platform in context")
	gomega.Expect(tokenPlatform.GetTopology()).ToNot(gomega.BeNil(), "invalid token topology, it is nil")
	gomega.Expect(len(tokenPlatform.GetTopology().TMSs)).ToNot(gomega.BeEquivalentTo(0), "no tms defined in token topology")
	// Gen crypto material for the new issuer wallet
	newIssuerWalletPath := tokenPlatform.GenIssuerCryptoMaterial(tokenPlatform.GetTopology().TMSs[0].BackendTopology.Name(), issuer.Id(), "issuer.ExtraId")
	// Register it
	RegisterIssuerIdentity(network, issuer, "newIssuerWallet", newIssuerWalletPath)
	// Update public parameters
	networkName := "default"
	tms := GetTMSByNetworkName(network, networkName)
	defaultTMSID := &token4.TMSID{
		Network:   tms.Network,
		Channel:   tms.Channel,
		Namespace: tms.Namespace,
	}

	newPP := PreparePublicParamsWithNewIssuer(network, newIssuerWalletPath, networkName)
	UpdatePublicParamsAndWait(network, newPP, GetTMSByNetworkName(network, networkName), alice, bob, charlie, manager, issuer, auditor, custodian)
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	// Issuer tokens with this new wallet
	t4 := time.Now()
	IssueCash(network, "newIssuerWallet", "EUR", 10, bob, auditor, false, issuer)
	// t5 := time.Now()
	CheckBalanceAndHolding(network, bob, "", "EUR", 10, auditor)
	IssueCash(network, "newIssuerWallet", "EUR", 10, bob, auditor, true, issuer)
	// t6 := time.Now()
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)
	IssueCash(network, "newIssuerWallet", "EUR", 10, bob, auditor, false, issuer)
	t7 := time.Now()
	CheckBalanceAndHolding(network, bob, "", "EUR", 30, auditor)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:5], nil, nil)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:5], &t0, &t7)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[2:5], &t4, &t7)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[:3], nil, nil, nil)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[:3], &t4, &t7, nil)

	h = ListIssuerHistory(network, "", "USD", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(120))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType("USD").Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "newIssuerWallet", "EUR", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(30))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType("EUR").Count()).To(gomega.BeEquivalentTo(h.Count()))

	CheckBalanceAndHolding(network, alice, "", "USD", 120, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 30, auditor)

	Restart(network, false, onRestart, alice)

	t8 := time.Now()
	TransferCash(network, alice, "", "USD", 111, bob, auditor)
	t9 := time.Now()
	CheckAuditedTransactions(network, auditor, AuditedTransactions[5:7], &t8, &t9)
	CheckSpending(network, alice, "", "USD", auditor, 111)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[3:4], &t8, &t9, nil)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[3:4], &t8, &t9, nil, ttxdb.Transfer)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[3:4], &t8, &t9, []ttxdb.TxStatus{ttxdb.Confirmed}, ttxdb.Transfer)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[:4], &t0, &t9, nil)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[:4], nil, nil, nil)
	ut := ListUnspentTokens(network, alice, "", "USD")
	gomega.Expect(ut.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(ut.Sum(64).ToBigInt().Cmp(big.NewInt(9))).To(gomega.BeEquivalentTo(0), "got [%d], expected 9", ut.Sum(64).ToBigInt())
	gomega.Expect(ut.ByType("USD").Count()).To(gomega.BeEquivalentTo(ut.Count()))
	ut = ListUnspentTokens(network, bob, "", "USD")
	gomega.Expect(ut.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(ut.Sum(64).ToBigInt().Cmp(big.NewInt(111))).To(gomega.BeEquivalentTo(0), "got [%d], expected 111", ut.Sum(64).ToBigInt())
	gomega.Expect(ut.ByType("USD").Count()).To(gomega.BeEquivalentTo(ut.Count()))

	AuthorizeRedeemIssuer(network, auditor, GetIssuerIdentity(tms, issuer.Id()))
	RedeemCashForTMSID(network, bob, "", "USD", 11, auditor, issuer, defaultTMSID)
	t10 := time.Now()
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[:6], nil, nil, nil)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[5:6], nil, nil, nil, ttxdb.Redeem)
	CheckAcceptedTransactions(network, bob, "", BobAcceptedTransactions[5:6], nil, nil, []ttxdb.TxStatus{ttxdb.Confirmed}, ttxdb.Redeem)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[7:9], &t9, &t10)

	t11 := time.Now()
	IssueCash(network, "", "USD", 10, bob, auditor, true, issuer)
	t12 := time.Now()
	CheckAuditedTransactions(network, auditor, AuditedTransactions[9:10], &t11, &t12)
	CheckAuditedTransactions(network, auditor, AuditedTransactions[:10], &t0, &t12)
	CheckSpending(network, bob, "", "USD", auditor, 11)

	// test multi action transfer...
	t13 := time.Now()
	IssueCash(network, "", "LIRA", 3, alice, auditor, true, issuer)
	IssueCash(network, "", "LIRA", 3, alice, auditor, true, issuer)
	t14 := time.Now()
	CheckAuditedTransactions(network, auditor, AuditedTransactions[10:12], &t13, &t14)
	// perform the normal transaction
	txLiraTransfer := TransferCashMultiActions(network, alice, "", "LIRA", []uint64{2, 3}, []*token3.NodeReference{bob, charlie}, auditor, nil)
	t16 := time.Now()
	AuditedTransactions[12].TxID = txLiraTransfer
	AuditedTransactions[13].TxID = txLiraTransfer
	AuditedTransactions[14].TxID = txLiraTransfer
	CheckBalanceAndHolding(network, alice, "", "LIRA", 1, auditor)
	CheckBalanceAndHolding(network, bob, "", "LIRA", 2, auditor)
	CheckBalanceAndHolding(network, charlie, "", "LIRA", 3, auditor)
	CheckAuditedTransactions(network, auditor, AuditedTransactions, &t0, &t16)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)

	IssueCash(network, "", "USD", 1, alice, auditor, true, issuer)

	testTwoGeneratedOwnerWalletsSameNode(network, auditor, !aries, sel, onRestart)

	CheckBalanceAndHolding(network, alice, "", "USD", 10, auditor)
	CheckBalanceAndHolding(network, alice, "", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 30, auditor)
	CheckBalanceAndHolding(network, bob, "bob", "EUR", 30, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)

	SwapCash(network, alice, "", "USD", 10, "EUR", 10, bob, auditor)

	CheckBalanceAndHolding(network, alice, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, alice, "", "EUR", 10, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 120, auditor)
	CheckSpending(network, alice, "", "USD", auditor, 121)
	CheckSpending(network, bob, "", "EUR", auditor, 10)

	// The following RedeemCash doesn't specify the issuer's network id so
	// it must be preceded by binding this network id with the issuer's signing id
	// so the endorsement process could automatically identify the issuer
	// that needs to sign the Redeem.
	BindIssuerNetworkAndSigningIdentities(network, issuer, GetIssuerIdentity(GetTMSByNetworkName(network, networkName), issuer.Id()), bob)
	RedeemCashForTMSID(network, bob, "", "USD", 10, auditor, nil, defaultTMSID)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)
	CheckSpending(network, bob, "", "USD", auditor, 21)

	// Check self endpoints
	IssueCash(network, "", "USD", 110, issuer, auditor, true, issuer)
	IssueCash(network, "newIssuerWallet", "EUR", 150, issuer, auditor, true, issuer)
	IssueCash(network, "issuer.id1", "EUR", 10, sel.Get("issuer.owner"), auditor, true, issuer)

	h = ListIssuerHistory(network, "", "USD", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(241))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType("USD").Count()).To(gomega.BeEquivalentTo(h.Count()))

	h = ListIssuerHistory(network, "newIssuerWallet", "EUR", issuer)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(180))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType("EUR").Count()).To(gomega.BeEquivalentTo(h.Count()))

	CheckBalanceAndHolding(network, issuer, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, issuer, "", "EUR", 150, auditor)
	CheckBalanceAndHolding(network, issuer, "issuer.owner", "EUR", 10, auditor)

	// Restart the auditor
	Restart(network, true, onRestart, auditor)
	RegisterAuditor(network, auditor)

	CheckBalanceAndHolding(network, issuer, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, issuer, "", "EUR", 150, auditor)
	CheckBalanceAndHolding(network, issuer, "issuer.owner", "EUR", 10, auditor)

	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// Check double spending
	txIDPine := IssueCash(network, "", "PINE", 55, alice, auditor, true, issuer)
	tokenIDPine := &token2.ID{
		TxId:  txIDPine,
		Index: 0,
	}
	txID1, tx1 := PrepareTransferCash(network, alice, "", "PINE", 55, bob, auditor, tokenIDPine)
	CheckBalance(network, alice, "", "PINE", 55)
	CheckHolding(network, alice, "", "PINE", 0, auditor)
	CheckBalance(network, bob, "", "PINE", 0)
	CheckHolding(network, bob, "", "PINE", 55, auditor)
	txID2, tx2 := PrepareTransferCash(network, alice, "", "PINE", 55, bob, auditor, tokenIDPine)
	CheckBalance(network, alice, "", "PINE", 55)
	CheckHolding(network, alice, "", "PINE", -55, auditor)
	CheckBalance(network, bob, "", "PINE", 0)
	CheckHolding(network, bob, "", "PINE", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)
	CheckOwnerStore(network, nil, bob)
	fmt.Printf("prepared transactions [%s:%s]", txID1, txID2)
	Restart(network, true, onRestart, bob)
	Restart(network, false, onRestart, auditor)
	RegisterAuditor(network, auditor)
	CheckBalance(network, bob, "", "PINE", 0)
	CheckHolding(network, bob, "", "PINE", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)
	BroadcastPreparedTransferCash(network, alice, txID1, tx1, true)
	common2.CheckFinality(network, bob, txID1, nil, false)
	common2.CheckFinality(network, auditor, txID1, nil, false)
	CheckBalance(network, alice, "", "PINE", 0)
	CheckHolding(network, alice, "", "PINE", -55, auditor)
	CheckBalance(network, bob, "", "PINE", 55)
	CheckHolding(network, bob, "", "PINE", 110, auditor)
	BroadcastPreparedTransferCash(network, alice, txID2, tx2, true, "is not valid")
	common2.CheckFinality(network, bob, txID2, nil, true)
	common2.CheckFinality(network, auditor, txID2, nil, true)
	CheckBalanceAndHolding(network, alice, "", "PINE", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "PINE", 55, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// Test Auditor ability to override transaction state
	txID3, tx3 := PrepareTransferCash(network, bob, "", "PINE", 10, alice, auditor, nil)
	CheckBalance(network, alice, "", "PINE", 0)
	CheckHolding(network, alice, "", "PINE", 10, auditor)
	CheckBalance(network, bob, "", "PINE", 55)
	CheckHolding(network, bob, "", "PINE", 45, auditor)
	SetTransactionAuditStatus(network, auditor, txID3, ttx.Deleted)
	CheckBalanceAndHolding(network, alice, "", "PINE", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "PINE", 55, auditor)
	TokenSelectorUnlock(network, bob, txID3)
	FinalityWithTimeout(network, bob, tx3, 20*time.Second)
	SetTransactionOwnersStatus(network, txID3, ttx.Deleted, token3.AllNames(alice, bob)...)

	// Restart
	CheckOwnerStore(network, nil, alice, bob)
	CheckOwnerStore(network, nil, issuer, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	Restart(network, false, onRestart, alice, bob, charlie, manager)
	CheckOwnerStore(network, nil, alice, bob)
	CheckOwnerStore(network, nil, issuer, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// Addition transfers
	TransferCash(network, issuer, "", "USD", 50, issuer, auditor)
	CheckBalanceAndHolding(network, issuer, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, issuer, "", "EUR", 150, auditor)

	TransferCash(network, issuer, "", "USD", 50, manager, auditor)
	TransferCash(network, issuer, "", "EUR", 20, manager, auditor)
	CheckBalanceAndHolding(network, issuer, "", "USD", 60, auditor)
	CheckBalanceAndHolding(network, issuer, "", "EUR", 130, auditor)
	CheckBalanceAndHolding(network, manager, "", "USD", 50, auditor)
	CheckBalanceAndHolding(network, manager, "", "EUR", 20, auditor)

	// Play with wallets
	TransferCash(network, manager, "", "USD", 10, sel.Get("manager.id1"), auditor)
	TransferCash(network, manager, "", "USD", 10, sel.Get("manager.id2"), auditor)
	TransferCash(network, manager, "", "USD", 10, sel.Get("manager.id3"), auditor)
	CheckBalanceAndHolding(network, manager, "", "USD", 20, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id1", "USD", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id2", "USD", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id3", "USD", 10, auditor)

	TransferCash(network, manager, "manager.id1", "USD", 10, sel.Get("manager.id2"), auditor)
	CheckSpending(network, manager, "manager.id1", "USD", auditor, 10)
	CheckBalanceAndHolding(network, manager, "", "USD", 20, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id1", "USD", 0, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id2", "USD", 20, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id3", "USD", 10, auditor)

	// Swap among wallets
	TransferCash(network, manager, "", "EUR", 10, sel.Get("manager.id1"), auditor)
	CheckBalanceAndHolding(network, manager, "", "EUR", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id1", "EUR", 10, auditor)

	SwapCash(network, manager, "manager.id1", "EUR", 10, "USD", 10, sel.Get("manager.id2"), auditor)
	CheckBalanceAndHolding(network, manager, "", "USD", 20, auditor)
	CheckBalanceAndHolding(network, manager, "", "EUR", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id1", "USD", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id1", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id2", "USD", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id2", "EUR", 10, auditor)
	CheckBalanceAndHolding(network, manager, "manager.id3", "USD", 10, auditor)

	// no more USD can be issued, reached quota of 220
	IssueCash(network, "", "USD", 10, alice, auditor, true, issuer, "no more USD can be issued, reached quota of 241")

	CheckBalanceAndHolding(network, alice, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, alice, "", "EUR", 10, auditor)

	// limits
	CheckBalanceAndHolding(network, alice, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, alice, "", "EUR", 10, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)
	IssueCash(network, "", "EUR", 2200, alice, auditor, true, issuer)
	IssueCash(network, "", "EUR", 2000, charlie, auditor, true, issuer)
	CheckBalanceAndHolding(network, alice, "", "EUR", 2210, auditor)
	CheckBalanceAndHolding(network, charlie, "", "EUR", 2000, auditor)
	TransferCash(network, alice, "", "EUR", 210, bob, auditor, "payment limit reached", "alice", "[EUR][210]")
	CheckBalanceAndHolding(network, alice, "", "EUR", 2210, auditor)
	CheckBalanceAndHolding(network, charlie, "", "EUR", 2000, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 20, auditor)

	PruneInvalidUnspentTokens(network, issuer, auditor, alice, bob, charlie, manager)

	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 1820, auditor)
	CheckSpending(network, alice, "", "EUR", auditor, 1800)
	TransferCash(network, alice, "", "EUR", 200, bob, auditor, "cumulative payment limit reached", "alice", "[EUR][2000]")

	TransferCash(network, charlie, "", "EUR", 200, bob, auditor)
	PruneInvalidUnspentTokens(network, issuer, auditor, alice, bob, charlie, manager)
	TransferCash(network, charlie, "", "EUR", 200, bob, auditor)
	TransferCash(network, charlie, "", "EUR", 200, bob, auditor)
	TransferCash(network, charlie, "", "EUR", 200, bob, auditor)
	TransferCash(network, charlie, "", "EUR", 200, bob, auditor)
	CheckBalanceAndHolding(network, charlie, "", "EUR", 1000, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 2820, auditor)
	TransferCash(network, charlie, "", "EUR", 200, bob, auditor, "holding limit reached", "bob", "[EUR][3020]")
	CheckBalanceAndHolding(network, bob, "", "EUR", 2820, auditor)

	PruneInvalidUnspentTokens(network, issuer, auditor, alice, bob, charlie, manager)

	// Routing
	IssueCash(network, "", "EUR", 10, sel.Get("alice.id1"), auditor, true, issuer)
	CheckAcceptedTransactions(network, alice, "alice.id1", AliceID1AcceptedTransactions, nil, nil, nil)
	TransferCash(network, alice, "alice.id1", "EUR", 10, sel.Get("bob.id1"), auditor)
	CheckBalanceAndHolding(network, alice, "alice.id1", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "bob.id1", "EUR", 10, auditor)

	// Concurrent transfers
	transferErrors := make([]chan error, 5)
	var sum uint64
	for i := range transferErrors {
		transferErrors[i] = make(chan error, 1)

		transfer := transferErrors[i]
		r, err := rand.Int(rand.Reader, big.NewInt(200))
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		v := r.Uint64() + 1
		sum += v
		go func() {
			_, err := network.Client(bob.ReplicaName()).CallView("transferWithSelector", common.JSONMarshall(&views.Transfer{
				Auditor:      auditor.Id(),
				Wallet:       "",
				Type:         "EUR",
				Amount:       v,
				Recipient:    network.Identity(alice.Id()),
				RecipientEID: alice.Id(),
				Retry:        true,
			}))
			if err != nil {
				transfer <- err

				return
			}
			transfer <- nil
		}()
	}
	for _, transfer := range transferErrors {
		var err error
		select {
		case err = <-transfer:
			// Received transfer result
		case <-time.After(transferTimeout):
			gomega.Expect(false).To(gomega.BeTrue(), "timeout waiting for transfer to complete")
		}
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
	}
	CheckBalanceAndHolding(network, bob, "", "EUR", 2820-sum, auditor)

	// Transfer With TokenSelector
	IssueSuccessfulCash(network, "", "YUAN", 17, alice, auditor, true, issuer, endorsers...)
	TransferCashWithSelector(network, alice, "", "YUAN", 10, bob, auditor)
	CheckBalanceAndHolding(network, alice, "", "YUAN", 7, auditor)
	CheckBalanceAndHolding(network, bob, "", "YUAN", 10, auditor)
	TransferCashWithSelector(network, alice, "", "YUAN", 10, bob, auditor, "pineapple", "insufficient funds")

	// Now, the tests asks Bob to transfer to Charlie 14 YUAN split in two parallel transactions each one transferring 7 YUAN.
	// Notice that Bob has only 10 YUAN, therefore bob will be able to assemble only one transfer.
	// We use two channels to collect the results of the two transfers.
	transferErrors2 := make([]chan error, 2)
	for i := range transferErrors2 {
		transferErrors2[i] = make(chan error, 1)

		transferError := transferErrors2[i]
		go func() {
			txid, err := network.Client(bob.ReplicaName()).CallView("transferWithSelector", common.JSONMarshall(&views.Transfer{
				Auditor:      auditor.Id(),
				Wallet:       "",
				Type:         "YUAN",
				Amount:       7,
				Recipient:    network.Identity(charlie.Id()),
				RecipientEID: charlie.Id(),
				Retry:        false,
			}))
			if err != nil {
				// The transaction failed, we return the error to the caller.
				transferError <- err

				return
			}
			// The transaction didn't fail, let's wait for it to be confirmed, and return no error
			common2.CheckFinality(network, charlie, common.JSONUnmarshalString(txid), nil, false)
			common2.CheckFinality(network, auditor, common.JSONUnmarshalString(txid), nil, false)
			transferError <- nil
		}()
	}
	// collect the errors, and check that they are all nil, and one of them is the error we expect.
	var errs []error
	for _, transfer := range transferErrors2 {
		var err error
		select {
		case err = <-transfer:
			// Received transfer result
		case <-time.After(transferTimeout):
			gomega.Expect(false).To(gomega.BeTrue(), "timeout waiting for transfer to complete")
		}
		errs = append(errs, err)
	}
	gomega.Expect((errs[0] == nil && errs[1] != nil) || (errs[0] != nil && errs[1] == nil)).To(gomega.BeTrue())
	var errStr string
	if errs[0] == nil {
		errStr = errs[1].Error()
	} else {
		errStr = errs[0].Error()
	}
	v := strings.Contains(errStr, "pineapple") || strings.Contains(errStr, "lemonade")
	gomega.Expect(v).To(gomega.BeEquivalentTo(true), "error [%s] does not contain either 'pineapple' or 'lemonade'", errStr)
	gomega.Expect(errStr).NotTo(gomega.BeEmpty())

	CheckBalanceAndHolding(network, bob, "", "YUAN", 3, auditor)
	CheckBalanceAndHolding(network, alice, "", "YUAN", 7, auditor)
	CheckBalanceAndHolding(network, charlie, "", "YUAN", 7, auditor)

	// Transfer by IDs
	{
		txID1 := IssueCash(network, "", "CHF", 17, alice, auditor, true, issuer)
		TransferCashByIDs(network, alice, "", []*token2.ID{{TxId: txID1, Index: 0}}, 17, bob, auditor, true, "test release")
		// the previous call should not keep the token locked if release is successful
		txID2 := TransferCashByIDs(network, alice, "", []*token2.ID{{TxId: txID1, Index: 0}}, 17, bob, auditor, false)
		WhoDeletedToken(network, alice, []*token2.ID{{TxId: txID1, Index: 0}}, txID2)
		WhoDeletedToken(network, auditor, []*token2.ID{{TxId: txID1, Index: 0}}, txID2)
		// redeem newly created token
		RedeemCashByIDs(network, networkName, bob, "", []*token2.ID{{TxId: txID2, Index: 0}}, 17, auditor, issuer)
	}

	PruneInvalidUnspentTokens(network, issuer, auditor, alice, bob, charlie, manager)

	// Test Max Token Value
	IssueCash(network, "", "MAX", 65535, charlie, auditor, true, issuer)
	IssueCash(network, "", "MAX", 65535, charlie, auditor, true, issuer)
	TransferCash(network, charlie, "", "MAX", 65536, alice, auditor, "failed to convert [65536] to quantity of precision [16]")
	IssueCash(network, "", "MAX", 65536, charlie, auditor, true, issuer, "q is larger than max token value [65535]")

	// Check consistency
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)
	CheckOwnerStore(network, nil, issuer, auditor, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	PruneInvalidUnspentTokens(network, issuer, auditor, alice, bob, charlie, manager)

	for _, ref := range []*token3.NodeReference{alice, bob, charlie, manager} {
		IDs := ListVaultUnspentTokens(network, ref)
		CheckIfExistsInVault(network, auditor, IDs)
	}

	// Check double spending by multiple action in the same transaction

	// use the same token for both actions, this must fail
	txIssuedPineapples1 := IssueCash(network, "", "Pineapples", 3, alice, auditor, true, issuer)
	IssueCash(network, "", "Pineapples", 3, alice, auditor, true, issuer)
	failedTransferTxID := TransferCashMultiActions(
		network,
		alice,
		"",
		"Pineapples",
		[]uint64{2, 3},
		[]*token3.NodeReference{bob, charlie},
		auditor,
		&token2.ID{TxId: txIssuedPineapples1},
		fmt.Sprintf("duplicate token ID [[%s:0]] found in metadata at action index [1] for tx", txIssuedPineapples1),
		txIssuedPineapples1,
	)
	// the above transfer must fail at execution phase, therefore the auditor should be explicitly informed about this transaction
	CheckBalance(network, alice, "", "Pineapples", 6)
	CheckHolding(network, alice, "", "Pineapples", 6, auditor)
	CheckBalance(network, bob, "", "Pineapples", 0)
	CheckHolding(network, bob, "", "Pineapples", 0, auditor)
	CheckBalance(network, charlie, "", "Pineapples", 0)
	CheckHolding(network, charlie, "", "Pineapples", 0, auditor)
	fmt.Printf("failed transaction [%s]\n", failedTransferTxID)
	SetTransactionAuditStatus(network, auditor, failedTransferTxID, ttx.Deleted)
	CheckBalanceAndHolding(network, alice, "", "Pineapples", 6, auditor)
	CheckBalanceAndHolding(network, bob, "", "Pineapples", 0, auditor)
	CheckBalanceAndHolding(network, charlie, "", "Pineapples", 0, auditor)
	CheckAuditorStore(network, auditor, "", nil)

	// test spendable token
	txIssueSpendableToken := IssueCash(network, "", "Spendable", 3, alice, auditor, true, issuer)
	SetSpendableFlag(network, alice, token2.ID{TxId: txIssueSpendableToken, Index: 0}, false)
	TransferCash(network, alice, "", "Spendable", 2, bob, auditor, "failed selecting tokens")
	SetSpendableFlag(network, alice, token2.ID{TxId: txIssueSpendableToken, Index: 0}, true)
	TransferCash(network, alice, "", "Spendable", 2, bob, auditor)
	CheckBalanceAndHolding(network, alice, "", "Spendable", 1, auditor)
	CheckBalanceAndHolding(network, bob, "", "Spendable", 2, auditor)
	CheckAuditorStore(network, auditor, "", nil)
}

func TestSelector(network *integration.Infrastructure, auditorId string, sel *token3.ReplicaSelector) {
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())

	IssueCash(network, "", "USD", 100, alice, auditor, true, issuer)
	IssueCash(network, "", "USD", 50, alice, auditor, true, issuer)
	TransferCash(network, alice, "", "USD", 160, bob, auditor, "insufficient funds, only [150] tokens of type [USD] are available")
	time.Sleep(10 * time.Second)
	TransferCash(network, alice, "", "USD", 160, bob, auditor, "insufficient funds, only [150] tokens of type [USD] are available")
	time.Sleep(2 * time.Minute)
	TransferCash(network, alice, "", "USD", 160, bob, auditor, "insufficient funds, only [150] tokens of type [USD] are available")
}

func TestPublicParamsUpdate(network *integration.Infrastructure, newAuditorID string, ppBytes []byte, networkName string, issuerAsAuditor bool, sel *token3.ReplicaSelector, updateWithAppend bool) {
	newAuditor := sel.Get(newAuditorID)
	tms := GetTMSByNetworkName(network, networkName)
	newIssuer := sel.Get("newIssuer")
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	manager := sel.Get("manager")
	auditor := sel.Get("auditor")
	if issuerAsAuditor {
		auditor = issuer
	}
	RegisterAuditor(network, auditor)
	txId := IssueCash(network, "", "USD", 110, alice, auditor, true, issuer)
	gomega.Expect(txId).NotTo(gomega.BeEmpty())
	CheckBalanceAndHolding(network, alice, "", "USD", 110, auditor)

	RegisterAuditor(network, newAuditor)
	UpdatePublicParams(network, ppBytes, tms)

	gomega.Eventually(GetPublicParams).WithArguments(network, newIssuer).WithTimeout(30 * time.Second).WithPolling(15 * time.Second).Should(gomega.Equal(ppBytes))
	gomega.Eventually(GetPublicParams).WithArguments(network, issuer).WithTimeout(30 * time.Second).WithPolling(15 * time.Second).Should(gomega.Equal(ppBytes))
	if !issuerAsAuditor {
		gomega.Eventually(GetPublicParams).WithArguments(network, newAuditor).WithTimeout(30 * time.Second).WithPolling(15 * time.Second).Should(gomega.Equal(ppBytes))
	}
	// give time to the issuer and the auditor to update their public parameters and reload their wallets
	gomega.Eventually(DoesWalletExist).WithArguments(network, newIssuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	if issuerAsAuditor {
		gomega.Eventually(DoesWalletExist).WithArguments(network, newIssuer, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	} else {
		gomega.Eventually(DoesWalletExist).WithArguments(network, newAuditor, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	}
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, manager, "manager.id1", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	txId = IssueCash(network, "", "USD", 110, alice, newAuditor, true, newIssuer)
	gomega.Expect(txId).NotTo(gomega.BeEmpty())
	CheckBalance(network, alice, "", "USD", 220)
	CheckHolding(network, alice, "", "USD", 110, newAuditor)
	if updateWithAppend {
		IssueCash(network, "", "USD", 110, alice, newAuditor, true, issuer)
	} else {
		IssueCash(network, "", "USD", 110, alice, newAuditor, true, issuer, validator.ErrIssuerNotAuthorized.Error())
	}
	if newAuditorID != "auditor" {
		if updateWithAppend {
			IssueCash(network, "", "USD", 110, alice, auditor, true, newIssuer)
		} else {
			IssueCashWithNoAuditorSigVerification(network, "", "USD", 110, alice, auditor, true, newIssuer, "is not in auditors")
		}
	}

	CheckOwnerWalletIDs(network, manager, "manager.id1", "manager.id2", "manager.id3")
}

func testTwoGeneratedOwnerWalletsSameNode(network *integration.Infrastructure, auditor *token3.NodeReference, useFabricCA bool, sel *token3.ReplicaSelector, onRestart OnRestartFunc) {
	ctx := context.Background()
	issuer := sel.Get("issuer")
	charlie := sel.Get("charlie")

	tokenPlatform := token.GetPlatform(network.NWOCtx, "token")
	idConfig1 := tokenPlatform.GenOwnerCryptoMaterial(tokenPlatform.GetTopology().TMSs[0].BackendTopology.Name(), charlie.Id(), "charlie.ExtraId1", false)
	RegisterOwnerIdentity(ctx, network, charlie, idConfig1)
	idConfig2 := tokenPlatform.GenOwnerCryptoMaterial(tokenPlatform.GetTopology().TMSs[0].BackendTopology.Name(), charlie.Id(), "charlie.ExtraId2", useFabricCA)
	RegisterOwnerIdentity(ctx, network, charlie, idConfig2)

	IssueCash(network, "", "SPE", 100, charlie, auditor, true, issuer)
	TransferCash(network, charlie, "", "SPE", 25, sel.Get("charlie.ExtraId1"), auditor)
	Restart(network, false, onRestart, charlie)
	TransferCash(network, charlie, "charlie.ExtraId1", "SPE", 15, sel.Get("charlie.ExtraId2"), auditor)

	CheckBalanceAndHolding(network, charlie, "", "SPE", 75, auditor)
	CheckBalanceAndHolding(network, charlie, "charlie.ExtraId1", "SPE", 10, auditor)
	CheckBalanceAndHolding(network, charlie, "charlie.ExtraId2", "SPE", 15, auditor)
}

func TestRevokeIdentity(network *integration.Infrastructure, auditorId string, sel *token3.ReplicaSelector) {
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")

	RegisterAuditor(network, auditor)

	IssueCash(network, "", "USD", 110, alice, auditor, true, issuer)
	CheckBalanceAndHolding(network, alice, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "bob.id1", "USD", 0, auditor)

	rId := GetRevocationHandle(network, bob)
	RevokeIdentity(network, auditor, rId)
	// try to issue to bob
	IssueCash(network, "", "USD", 22, bob, auditor, true, issuer, fsclogging.SHA256Base64([]byte(rId)).String()+" Identity is in revoked state")
	// try to transfer to bob
	TransferCash(network, alice, "", "USD", 22, bob, auditor, fsclogging.SHA256Base64([]byte(rId)).String()+" Identity is in revoked state")
	CheckBalanceAndHolding(network, alice, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "bob.id1", "USD", 0, auditor)

	// Issuer to bob.id1
	IssueCash(network, "", "USD", 90, sel.Get("bob.id1"), auditor, true, issuer)
	CheckBalanceAndHolding(network, alice, "", "USD", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "bob.id1", "USD", 90, auditor)
}

func TestMixed(network *integration.Infrastructure, onRestart OnRestartFunc, sel *token3.ReplicaSelector) {
	auditor1 := sel.Get("auditor1")
	auditor2 := sel.Get("auditor2")
	issuer1 := sel.Get("issuer1")
	issuer2 := sel.Get("issuer2")
	alice := sel.Get("alice")
	bob := sel.Get("bob")

	dlogId := getTmsId(network, DLogNamespace)
	fabTokenId := getTmsId(network, FabTokenNamespace)
	RegisterAuditorForTMSID(network, auditor1, dlogId)
	RegisterAuditorForTMSID(network, auditor2, fabTokenId)

	// give some time to the nodes to get the public parameters
	time.Sleep(40 * time.Second)

	gomega.Eventually(CheckPublicParamsMatch).WithArguments(network, dlogId, issuer1, auditor1, alice, bob).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(CheckPublicParamsMatch).WithArguments(network, fabTokenId, issuer2, auditor2, alice, bob).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	IssueCashForTMSID(network, "", "USD", 110, alice, auditor1, true, issuer1, dlogId)
	IssueCashForTMSID(network, "", "USD", 115, alice, auditor2, true, issuer2, fabTokenId)

	TransferCashForTMSID(network, alice, "", "USD", 20, bob, auditor1, dlogId)
	TransferCashForTMSID(network, alice, "", "USD", 30, bob, auditor2, fabTokenId)

	AuthorizeRedeemIssuer(network, auditor1, GetIssuerIdentity(GetTMSByTMSID(network, *dlogId), issuer1.Id()))
	RedeemCashForTMSID(network, bob, "", "USD", 11, auditor1, issuer1, dlogId)
	CheckSpendingForTMSID(network, bob, "", "USD", auditor1, 11, dlogId)

	CheckBalanceAndHoldingForTMSID(network, alice, "", "USD", 90, auditor1, dlogId)
	CheckBalanceAndHoldingForTMSID(network, alice, "", "USD", 85, auditor2, fabTokenId)
	CheckBalanceAndHoldingForTMSID(network, bob, "", "USD", 9, auditor1, dlogId)
	CheckBalanceAndHoldingForTMSID(network, bob, "", "USD", 30, auditor2, fabTokenId)

	h := ListIssuerHistoryForTMSID(network, "", "USD", issuer1, dlogId)
	gomega.Expect(h.Count()).To(gomega.BeNumerically(">", 0))
	gomega.Expect(h.Sum(64).ToBigInt().Cmp(big.NewInt(110))).To(gomega.BeEquivalentTo(0))
	gomega.Expect(h.ByType("USD").Count()).To(gomega.BeEquivalentTo(h.Count()))

	// Error cases

	// Try to approve dlog with auditor2
	TransferCashForTMSID(network, alice, "", "USD", 20, bob, auditor2, dlogId, "")
	// Try to issue on dlog with issuer2
	IssueCashForTMSID(network, "", "USD", 110, alice, auditor1, true, issuer2, dlogId, "")
	// Try to spend on dlog coins from fabtoken
	TransferCashForTMSID(network, alice, "", "USD", 120, bob, auditor2, fabTokenId, "")
	// Try to issue more coins than the max
	IssueCashForTMSID(network, "", "MAX", 65535, bob, auditor1, true, issuer1, dlogId)
	IssueCashForTMSID(network, "", "MAX", 65536, bob, auditor2, true, issuer2, fabTokenId, "q is larger than max token value [65535]")

	// Shut down one auditor and try to issue cash for both chaincodes
	Restart(network, true, onRestart, auditor2)
	IssueCashForTMSID(network, "", "USD", 10, alice, auditor1, true, issuer1, dlogId)
	IssueCashForTMSID(network, "", "USD", 20, alice, auditor2, true, issuer2, fabTokenId, "")
	RegisterAuditor(network, auditor2)
	IssueCashForTMSID(network, "", "USD", 30, alice, auditor2, true, issuer2, fabTokenId)

	CheckBalanceAndHoldingForTMSID(network, alice, "", "USD", 100, auditor1, dlogId)
	CheckBalanceAndHoldingForTMSID(network, alice, "", "USD", 115, auditor2, fabTokenId)
}

func TestRemoteOwnerWallet(network *integration.Infrastructure, auditor string, sel *token3.ReplicaSelector, websSocket bool) {
	TestRemoteOwnerWalletWithWMP(network, NewWalletManagerProvider(&walletManagerLoader{II: network}), auditor, sel, websSocket)
}

func TestRemoteOwnerWalletWithWMP(network *integration.Infrastructure, wmp *WalletManagerProvider, auditorId string, sel *token3.ReplicaSelector, websSocket bool) {
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")

	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	Withdraw(network, wmp, websSocket, alice, "alice_remote", "USD", 10, auditor, issuer)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 10, auditor)

	TransferCashFromExternalWallet(network, wmp, websSocket, alice, "alice_remote", "USD", 7, bob, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 3, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote_2", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 7, auditor)
	TransferCashToExternalWallet(network, wmp, bob, "", "USD", 3, alice, "alice_remote", auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 6, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote_2", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 4, auditor)
	TransferCashFromExternalWallet(network, wmp, websSocket, alice, "alice_remote", "USD", 4, charlie, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 2, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote_2", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 4, auditor)
	CheckBalanceAndHolding(network, bob, "bob_remote", "USD", 0, auditor)
	CheckBalanceAndHolding(network, charlie, "", "USD", 4, auditor)
	TransferCashFromAndToExternalWallet(network, wmp, websSocket, alice, "alice_remote", "USD", 1, bob, "bob_remote", auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 1, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote_2", "USD", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 4, auditor)
	CheckBalanceAndHolding(network, bob, "bob_remote", "USD", 1, auditor)
	CheckBalanceAndHolding(network, charlie, "", "USD", 4, auditor)
	TransferCashFromAndToExternalWallet(network, wmp, websSocket, alice, "alice_remote", "USD", 1, alice, "alice_remote_2", auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote", "USD", 0, auditor)
	CheckBalanceAndHolding(network, alice, "alice_remote_2", "USD", 1, auditor)
	CheckBalanceAndHolding(network, bob, "", "USD", 4, auditor)
	CheckBalanceAndHolding(network, bob, "bob_remote", "USD", 1, auditor)
	CheckBalanceAndHolding(network, charlie, "", "USD", 4, auditor)
}

func TestMaliciousTransactions(net *integration.Infrastructure, sel *token3.ReplicaSelector) {
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	CheckPublicParams(net, issuer, alice, bob, charlie, manager)

	gomega.Eventually(DoesWalletExist).WithArguments(net, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(net, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(net, bob, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	IssueCash(net, "", "USD", 110, alice, sel.Get(""), true, issuer)
	CheckBalance(net, alice, "", "USD", 110)

	txID := MaliciousTransferCash(net, alice, "", "USD", 2, bob, sel.Get(""), nil)
	txStatusAlice := GetTXStatus(net, alice, txID)
	gomega.Expect(txStatusAlice.ValidationCode).To(gomega.BeEquivalentTo(ttx.Deleted))
	gomega.Expect(txStatusAlice.ValidationMessage).To(gomega.ContainSubstring("token requests do not match, tr hashes"))
	txStatusBob := GetTXStatus(net, bob, txID)
	gomega.Expect(txStatusBob.ValidationCode).To(gomega.BeEquivalentTo(ttx.Deleted))
	gomega.Expect(txStatusBob.ValidationMessage).To(gomega.ContainSubstring("token requests do not match, tr hashes"))
}

func TestStressSuite(network *integration.Infrastructure, auditorId string, selector *token3.ReplicaSelector) {
	r, err := nwo.NewSuiteExecutor(network, auditorId, "issuer")
	assert.NoError(err)

	assert.NoError(r.Execute([]model.SuiteConfig{{
		Name:       "stress-test-1",
		PoolSize:   10,
		Iterations: 2,
		Delay:      time.Second,
		Cases: []model.TestCase{
			{
				Name:   "test-case-1-1",
				Payer:  "alice",
				Payees: []model.UserAlias{"bob", "charlie"},
				Issue: model.IssueConfig{
					Total:        10,
					Distribution: "const:1",
					Execute:      true,
				},
				Transfer: model.TransferConfig{
					Distribution: "const:1",
					Execute:      true,
				},
			},
		},
		UseExistingFunds: false,
	}}))

	CheckLocalMetrics(network, "alice", "github.com/LFDT-Panurus/panurus/integration/token/fungible/views/BalanceView")
	CheckPrometheusMetrics(network, "github.com/LFDT-Panurus/panurus/integration/token/fungible/views/BalanceView")
}

func TestStress(network *integration.Infrastructure, auditorId string, selector *token3.ReplicaSelector) {
	auditor := selector.Get(auditorId)
	issuer := selector.Get("issuer")
	alice := selector.Get("alice")
	bob := selector.Get("bob")
	charlie := selector.Get("charlie")
	manager := selector.Get("manager")

	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditorId)
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	// Start Issuance
	issuePool := dlog.NewPool("issuer", 80)
	issuePool.ScheduleTask(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("caught panic during issue: %v", r)
			}
		}()
		IssueCash(network, "", "MAX", 10, alice, auditor, true, issuer)
		IssueCash(network, "", "MAX", 10, bob, auditor, true, issuer)
		IssueCash(network, "", "MAX", 10, charlie, auditor, true, issuer)
	})

	// let issue enough tokens
	time.Sleep(1 * time.Minute)

	// start transfers from Alice
	aliceTransferPool := dlog.NewPool("alice", 40)
	aliceTransferPool.ScheduleTask(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("caught panic during transfer alice to bob: %v", r)
			}
		}()
		TransferCashNoFinalityCheck(network, alice, "", "MAX", 2, bob, auditor)
	})

	time.Sleep(1 * time.Minute)

	// start transfers from Bob
	bobTransferPool := dlog.NewPool("bob", 40)
	bobTransferPool.ScheduleTask(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("caught panic during transfer bob to alice: %v", r)
			}
		}()
		TransferCashNoFinalityCheck(network, bob, "", "MAX", 1, alice, auditor)
	})

	// start transfers from Charlie
	charlieTransferPool := dlog.NewPool("charlie", 40)
	charlieTransferPool.ScheduleTask(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("caught panic during transfer charlie to alice: %v", r)
			}
		}()
		TransferCashNoFinalityCheck(network, charlie, "", "MAX", 1, charlie, auditor)
		TransferCashNoFinalityCheck(network, charlie, "", "MAX", 1, alice, auditor)
	})

	time.Sleep(1 * time.Minute)
	issuePool.Stop()
	aliceTransferPool.Stop()
	bobTransferPool.Stop()
	charlieTransferPool.Stop()

	issuePool.Wait()
	aliceTransferPool.Wait()
	bobTransferPool.Wait()
	charlieTransferPool.Wait()
}

func TestTokensUpgrade(network *integration.Infrastructure, auditorId string, onRestart OnRestartFunc, sel *token3.ReplicaSelector) {
	// we start with fabtoken 16bits, performs a few operation, and then switch
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	endorsers := GetEndorsers(network, sel)
	RegisterAuditor(network, auditor)
	tokenPlatform, ok := network.NWOCtx.PlatformsByName["token"].(*token.Platform)
	gomega.Expect(ok).To(gomega.BeTrue())

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	IssueSuccessfulCash(network, "", "EUR", 110, alice, auditor, true, issuer, endorsers...)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// switch to dlog 32bits, perform a few operations
	tms := GetTMSByAlias(network, "dlog-32bits")
	ppBytes, err := os.ReadFile(tokenPlatform.PublicParametersFile(tms))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ppBytes).NotTo(gomega.BeNil())
	UpdatePublicParams(network, ppBytes, tms)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, auditor, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	// alice has one token that cannot be spent directly, it was created when the fabtoken driver was in place
	CheckOwnerStore(network, func(errMsgs []string) error {
		if len(errMsgs) != 1 {
			return errors.Errorf("expected one error but got %v", errMsgs)
		}
		if !strings.Contains(errMsgs[0], "token format not supported [") {
			return errors.Errorf("expected error format not supported [%v]", errMsgs)
		}

		return nil
	}, alice)
	CheckOwnerStore(network, nil, issuer, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)

	TransferCash(network, alice, "", "EUR", 110, bob, auditor, "insufficient funds, only [0] tokens of type [EUR] are available, but [110] were requested and no other process has any tokens locked")
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)
	TokensUpgrade(network, nil, alice, "", "EUR", auditor, issuer)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)

	TransferCash(network, alice, "", "EUR", 110, bob, auditor)
	// CopyDBsTo(network, "./testdata", alice)
	// all the tokens are spendable now
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 110, auditor)
}

func TestLocalTokensUpgrade(network *integration.Infrastructure, auditorId string, onRestart OnRestartFunc, sel *token3.ReplicaSelector) {
	// we start with fabtoken 16bits, performs a few operation, and then switch
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	endorsers := GetEndorsers(network, sel)
	RegisterAuditor(network, auditor)
	tokenPlatform, ok := network.NWOCtx.PlatformsByName["token"].(*token.Platform)
	gomega.Expect(ok).To(gomega.BeTrue())

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	IssueSuccessfulCash(network, "", "EUR", 110, alice, auditor, true, issuer, endorsers...)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// switch to dlog 32bits, perform a few operations
	tms := GetTMSByAlias(network, "dlog-32bits")
	ppBytes, err := os.ReadFile(tokenPlatform.PublicParametersFile(tms))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ppBytes).NotTo(gomega.BeNil())
	UpdatePublicParams(network, ppBytes, tms)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, auditor, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)

	TransferCash(network, alice, "", "EUR", 110, bob, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 110, auditor)
}

func TestIdemixIssuerPublicKeyRotation(network *integration.Infrastructure, auditorId string, onRestart OnRestartFunc, sel *token3.ReplicaSelector) {
	// we start with fabtoken 16bits, performs a few operation, and then switch
	auditor := sel.Get(auditorId)
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	endorsers := GetEndorsers(network, sel)
	RegisterAuditor(network, auditor)
	tokenPlatform, ok := network.NWOCtx.PlatformsByName["token"].(*token.Platform)
	gomega.Expect(ok).To(gomega.BeTrue())

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	IssueSuccessfulCash(network, "", "EUR", 110, alice, auditor, true, issuer, endorsers...)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)

	// switch to dlog 32bits, perform a few operations
	tms := GetTMSByAlias(network, "dlog-32bits")
	ppBytes, err := os.ReadFile(tokenPlatform.PublicParametersFile(tms))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ppBytes).NotTo(gomega.BeNil())

	UpdatePublicParams(network, ppBytes, tms)
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, auditor, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)

	TransferCash(network, alice, "", "EUR", 110, bob, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 110, auditor)

	// rotate issuer public key, bob should be able to spend his token
	pp, err := dlognoghv1.NewPublicParamsFromBytes(ppBytes, dlognoghv1.DLogNoGHDriverName, dlognoghv1.ProtocolV1)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(pp.Validate()).NotTo(gomega.HaveOccurred())

	tmsBis := GetTMSByAlias(network, "dlog-32bits-bis")
	ppBytesBis, err := os.ReadFile(tokenPlatform.PublicParametersFile(tmsBis))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ppBytesBis).NotTo(gomega.BeNil())
	ppBis, err := dlognoghv1.NewPublicParamsFromBytes(ppBytesBis, dlognoghv1.DLogNoGHDriverName, dlognoghv1.ProtocolV1)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(ppBis.Validate()).NotTo(gomega.HaveOccurred())

	pp.IdemixIssuerPublicKeys = append(pp.IdemixIssuerPublicKeys, ppBis.IdemixIssuerPublicKeys...)
	ppRaw, err := pp.Serialize()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	UpdatePublicParams(network, ppRaw, tms)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, alice, "mango", views.OwnerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())
	gomega.Eventually(DoesWalletExist).WithArguments(network, auditor, "", views.AuditorWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())

	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 0, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 110, auditor)

	TransferCash(network, bob, "", "EUR", 110, alice, auditor)
	CheckOwnerStore(network, nil, issuer, alice, bob, charlie, manager)
	CheckAuditorStore(network, auditor, "", nil)
	CheckBalanceAndHolding(network, alice, "", "EUR", 110, auditor)
	CheckBalanceAndHolding(network, bob, "", "EUR", 0, auditor)
	CopyDBsTo(network, "./testdata", alice)
}

func TestMultiSig(network *integration.Infrastructure, sel *token3.ReplicaSelector) {
	auditor := sel.Get("auditor")
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	IssueCash(network, "", "USD", 110, alice, auditor, true, issuer)
	CheckBalance(network, alice, "", "USD", 110)
	CheckHolding(network, alice, "", "USD", 110, auditor)

	MultiSigLockCash(network, alice, "", "USD", 50, []*token3.NodeReference{bob, charlie}, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckBalance(network, manager, "", "USD", 0)
	CheckCoOwnedBalance(network, alice, "", "USD", 0)
	CheckCoOwnedBalance(network, bob, "", "USD", 50)
	CheckCoOwnedBalance(network, charlie, "", "USD", 50)
	CheckCoOwnedBalance(network, manager, "", "USD", 0)

	MultiSigSpendCash(network, bob, "", "USD", manager, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckBalance(network, manager, "", "USD", 50)
	CheckCoOwnedBalance(network, alice, "", "USD", 0)
	CheckCoOwnedBalance(network, bob, "", "USD", 0)
	CheckCoOwnedBalance(network, charlie, "", "USD", 0)
	CheckCoOwnedBalance(network, manager, "", "USD", 0)
}

// TestPolicyOR exercises a two-party OR policy ($0 OR $1): alice locks tokens under
// the policy identity, then bob spends them alone without involving charlie.
func TestPolicyOR(network *integration.Infrastructure, sel *token3.ReplicaSelector) {
	auditor := sel.Get("auditor")
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	RegisterAuditor(network, auditor)

	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	IssueCash(network, "", "USD", 110, alice, auditor, true, issuer)
	CheckBalance(network, alice, "", "USD", 110)
	CheckHolding(network, alice, "", "USD", 110, auditor)

	// Alice locks 50 USD under policy "$0 OR $1" with bob as $0 and charlie as $1.
	PolicyLockCash(network, alice, "", "USD", 50, "$0 OR $1", []*token3.NodeReference{bob, charlie}, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckHolding(network, alice, "", "USD", 60, auditor)
	CheckHolding(network, bob, "", "USD", 0, auditor)
	CheckHolding(network, charlie, "", "USD", 0, auditor)
	CheckPolicyOwnedBalance(network, bob, "", "USD", 50)
	CheckPolicyOwnedBalance(network, charlie, "", "USD", 50)

	// Bob alone can satisfy "$0 OR $1" and spends to manager.
	PolicySpendCashOR(network, bob, "", "USD", manager, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckBalance(network, manager, "", "USD", 50)
	CheckHolding(network, alice, "", "USD", 60, auditor)
	CheckHolding(network, bob, "", "USD", 0, auditor)
	CheckHolding(network, charlie, "", "USD", 0, auditor)
	CheckPolicyOwnedBalance(network, bob, "", "USD", 0)
	CheckPolicyOwnedBalance(network, charlie, "", "USD", 0)
	CheckHolding(network, manager, "", "USD", 50, auditor)
}

// TestPolicyAND exercises a two-party AND policy ($0 AND $1): alice locks tokens under
// the policy identity, then bob initiates a spend that charlie must also endorse.
func TestPolicyAND(network *integration.Infrastructure, sel *token3.ReplicaSelector) {
	auditor := sel.Get("auditor")
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	charlie := sel.Get("charlie")
	manager := sel.Get("manager")
	RegisterAuditor(network, auditor)

	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice, bob, charlie, manager)

	IssueCash(network, "", "USD", 110, alice, auditor, true, issuer)
	CheckBalance(network, alice, "", "USD", 110)
	CheckHolding(network, alice, "", "USD", 110, auditor)

	// Alice locks 50 USD under policy "$0 AND $1" with bob as $0 and charlie as $1.
	PolicyLockCash(network, alice, "", "USD", 50, "$0 AND $1", []*token3.NodeReference{bob, charlie}, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckHolding(network, alice, "", "USD", 60, auditor)
	CheckHolding(network, bob, "", "USD", 0, auditor)
	CheckHolding(network, charlie, "", "USD", 0, auditor)
	CheckPolicyOwnedBalance(network, bob, "", "USD", 50)
	CheckPolicyOwnedBalance(network, charlie, "", "USD", 50)

	// Bob initiates the spend; charlie co-endorses via PolicyAcceptSpendView.
	PolicySpendCashAND(network, bob, "", "USD", manager, auditor)
	CheckBalance(network, alice, "", "USD", 60)
	CheckBalance(network, bob, "", "USD", 0)
	CheckBalance(network, charlie, "", "USD", 0)
	CheckBalance(network, manager, "", "USD", 50)
	CheckHolding(network, alice, "", "USD", 60, auditor)
	CheckHolding(network, bob, "", "USD", 0, auditor)
	CheckHolding(network, charlie, "", "USD", 0, auditor)
	CheckPolicyOwnedBalance(network, bob, "", "USD", 0)
	CheckPolicyOwnedBalance(network, charlie, "", "USD", 0)
	CheckHolding(network, manager, "", "USD", 50, auditor)
}

func TestRedeem(network *integration.Infrastructure, sel *token3.ReplicaSelector, networkName string) {
	auditor := sel.Get("auditor")
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	tms := GetTMSByNetworkName(network, networkName)
	defaultTMSID := &token4.TMSID{
		Network:   tms.Network,
		Channel:   tms.Channel,
		Namespace: tms.Namespace,
	}
	RegisterAuditor(network, auditor)

	// give some time to the nodes to get the public parameters - Q - may now be needed. waiting in UpdatePublicParamsAndWait.
	time.Sleep(10 * time.Second)

	SetKVSEntry(network, issuer, "auditor", auditor.Id())
	CheckPublicParams(network, issuer, auditor, alice)

	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeTrue())
	gomega.Eventually(DoesWalletExist).WithArguments(network, issuer, "pineapple", views.IssuerWallet).WithTimeout(1 * time.Minute).WithPolling(15 * time.Second).Should(gomega.BeFalse())

	IssueCash(network, "", "USD", 110, issuer, auditor, true, issuer)
	CheckBalance(network, issuer, "", "USD", 110)
	CheckHolding(network, issuer, "", "USD", 110, auditor)

	// The auditor has not authorized this issuer to approve redeems yet: the redeem must be rejected.
	RedeemCashForTMSID(network, issuer, "", "USD", 10, auditor, issuer, defaultTMSID, "not on the authorized issuers list")
	CheckBalance(network, issuer, "", "USD", 110)
	CheckHolding(network, issuer, "", "USD", 110, auditor)

	// Once the auditor authorizes the issuer, the redeem it approves succeeds.
	AuthorizeRedeemIssuer(network, auditor, GetIssuerIdentity(tms, issuer.Id()))
	RedeemCashForTMSID(network, issuer, "", "USD", 10, auditor, issuer, defaultTMSID)
	CheckBalance(network, issuer, "", "USD", 100)
	CheckHolding(network, issuer, "", "USD", 100, auditor)
}
