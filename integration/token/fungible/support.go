/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fungible

import (
	"context"
	"encoding/json"
	errors2 "errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	tplatform "github.com/LFDT-Panurus/panurus/integration/nwo/token"
	gfabtokenv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	dlognoghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	topology2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/reporting/prometheus"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fsclogging "github.com/hyperledger-labs/fabric-smart-client/platform/common/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/onsi/gomega"
	"github.com/prometheus/common/model"
)

var (
	RestartEnabled       = true
	eventualCheckTimeout = 30 * time.Second
	eventualCheckPolling = 1 * time.Second
)

type TransactionRecord struct {
	ttxdb.TransactionRecord
	CheckPrevious bool
	CheckNext     bool
}

type Stream interface {
	Recv(m any) error
	Send(m any) error
	Result() ([]byte, error)
}

func RegisterAuditor(network *integration.Infrastructure, auditor *token3.NodeReference) {
	RegisterAuditorForTMSID(network, auditor, nil)
}

func RegisterAuditorForTMSID(network *integration.Infrastructure, auditor *token3.NodeReference, tmsId *token2.TMSID) {
	_, err := network.Client(auditor.ReplicaName()).CallView("registerAuditor", common.JSONMarshall(&views.RegisterAuditor{
		TMSID: tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func getTmsId(network *integration.Infrastructure, namespace string) *token2.TMSID {
	fabricTopology := getFabricTopology(network)
	gomega.Expect(fabricTopology).NotTo(gomega.BeNil())

	return &token2.TMSID{
		Network:   fabricTopology.Name(),
		Channel:   fabricTopology.Channels[0].Name,
		Namespace: namespace,
	}
}

func getFabricTopology(network *integration.Infrastructure) *topology2.Topology {
	for _, t := range network.Topologies {
		if t.Type() == "fabric" {
			return t.(*topology2.Topology)
		}
	}

	return nil
}

func IssueCash(network *integration.Infrastructure, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, anonymous bool, issuer *token3.NodeReference, expectedErrorMsgs ...string) string {
	return IssueCashForTMSID(network, wallet, typ, amount, receiver, auditor, anonymous, issuer, nil, expectedErrorMsgs...)
}

func IssueSuccessfulCash(network *integration.Infrastructure, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, anonymous bool, issuer *token3.NodeReference, finalities ...*token3.NodeReference) string {
	return issueCashForTMSID(network, wallet, typ, amount, receiver, auditor, anonymous, issuer, nil, finalities, false, []string{})
}

func IssueCashForTMSID(network *integration.Infrastructure, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, anonymous bool, issuer *token3.NodeReference, tmsId *token2.TMSID, expectedErrorMsgs ...string) string {
	return issueCashForTMSID(network, wallet, typ, amount, receiver, auditor, anonymous, issuer, tmsId, []*token3.NodeReference{}, false, expectedErrorMsgs)
}

func IssueCashWithNoAuditorSigVerification(network *integration.Infrastructure, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, anonymous bool, issuer *token3.NodeReference, expectedErrorMsgs ...string) string {
	return issueCashForTMSID(network, wallet, typ, amount, receiver, auditor, anonymous, issuer, nil, []*token3.NodeReference{}, true, expectedErrorMsgs)
}

func issueCashForTMSID(
	network *integration.Infrastructure,
	wallet string,
	typ token.Type,
	amount uint64,
	receiver *token3.NodeReference,
	auditor *token3.NodeReference,
	anonymous bool,
	issuer *token3.NodeReference,
	tmsId *token2.TMSID,
	endorsers []*token3.NodeReference,
	skipAuditorSignatureVerification bool,
	expectedErrorMsgs []string,
) string {
	txIDBoxed, err := network.Client(issuer.ReplicaName()).CallView("issue", common.JSONMarshall(&views.IssueCash{
		Anonymous:                        anonymous,
		Auditor:                          auditor.Id(),
		IssuerWallet:                     wallet,
		TokenType:                        typ,
		Quantity:                         amount,
		Recipient:                        network.Identity(receiver.Id()),
		RecipientEID:                     receiver.Id(),
		TMSID:                            tmsId,
		SkipAuditorSignatureVerification: skipAuditorSignatureVerification,
	}))

	topology.ToOptions(network.FscPlatform.Peers[0].Options).Endorser()
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txIDBoxed)
		for _, n := range []*token3.NodeReference{receiver, auditor, issuer} {
			common2.CheckFinality(network, n, txID, tmsId, false)
		}
		// Perform this check only if there is a fabric network
		if getFabricTopology(network) != nil {
			for _, n := range endorsers {
				common2.CheckEndorserFinality(network, n, txID, tmsId, false)
			}
		}

		return common.JSONUnmarshalString(txIDBoxed)
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}

	return ""
}

func GetEndorsers(network *integration.Infrastructure, sel *token3.ReplicaSelector) []*token3.NodeReference {
	endorsers := make([]*token3.NodeReference, 0)
	for _, p := range network.FscPlatform.Peers {
		if topology.ToOptions(p.Options).Endorser() {
			endorsers = append(endorsers, sel.Get(p.Name))
		}
	}

	return endorsers
}

func CheckAuditedTransactions(network *integration.Infrastructure, auditor *token3.NodeReference, expected []TransactionRecord, start *time.Time, end *time.Time) {
	txsBoxed, err := network.Client(auditor.ReplicaName()).CallView("historyAuditing", common.JSONMarshall(&views.ListAuditedTransactions{
		From:            start,
		To:              end,
		SearchDirection: ttxdb.FromBeginning,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var txs []*ttxdb.TransactionRecord
	common.JSONUnmarshal(txsBoxed.([]byte), &txs)
	gomega.Expect(txs).To(gomega.HaveLen(len(expected)), "expected [%v] transactions, got [%v]. Params: start [%v], end [%v]", expected, txs, start, end)
	for i, tx := range txs {
		fmt.Printf("tx %d: %+v\n", i, tx)
		fmt.Printf("expected %d: %+v\n", i, expected[i])
	}
	// Records from the same transaction share a stored_at timestamp; their
	// order within that group is undefined, so match each group as a set.
	for lo := 0; lo < len(txs); {
		hi := lo + 1
		for hi < len(txs) && txs[hi].Timestamp.Equal(txs[lo].Timestamp) {
			hi++
		}
		matched := make([]bool, hi-lo)
		for _, txExpected := range expected[lo:hi] {
			found := false
			for k := range hi - lo {
				if !matched[k] && matchTransactionRecord(txs[lo+k], txExpected, lo+k) == nil {
					matched[k] = true
					found = true

					break
				}
			}
			gomega.Expect(found).To(gomega.BeTrue(), "no audited transaction matches expected [%+v] within stored_at group [%d:%d), got [%+v]", txExpected, lo, hi, txs[lo:hi])
		}
		lo = hi
	}
}

func CheckAcceptedTransactions(network *integration.Infrastructure, id *token3.NodeReference, wallet string, expected []TransactionRecord, start *time.Time, end *time.Time, statuses []ttxdb.TxStatus, actionTypes ...ttxdb.ActionType) {
	eIDBoxed, err := network.Client(id.ReplicaName()).CallView("GetEnrollmentID", common.JSONMarshall(&views.GetEnrollmentID{
		Wallet: wallet,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	eID := common.JSONUnmarshalString(eIDBoxed)

	params := views.ListAcceptedTransactions{
		SenderWallet:    eID,
		RecipientWallet: eID,
		From:            start,
		To:              end,
		ActionTypes:     actionTypes,
		Statuses:        statuses,
		SearchDirection: ttxdb.FromBeginning,
	}
	txsBoxed, err := network.Client(id.ReplicaName()).CallView("acceptedTransactionHistory", common.JSONMarshall(&params))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var txs []*ttxdb.TransactionRecord
	common.JSONUnmarshal(txsBoxed.([]byte), &txs)
	gomega.Expect(txs).To(gomega.HaveLen(len(expected)), "expected [%v] transactions, got [%v]. Params [%v]", expected, txs, params)
	for i, tx := range txs {
		fmt.Printf("tx %d: %+v\n", i, tx)
		fmt.Printf("expected %d: %+v\n", i, expected[i])
		txExpected := expected[i]
		err := matchTransactionRecord(tx, txExpected, i)
		if err != nil {
			if txExpected.CheckNext {
				gomega.Expect(matchTransactionRecord(tx, expected[i+1], i+1)).ToNot(gomega.HaveOccurred())

				continue
			}
			if txExpected.CheckPrevious {
				gomega.Expect(matchTransactionRecord(tx, expected[i-1], i-1)).ToNot(gomega.HaveOccurred())

				continue
			}
			gomega.Expect(false).To(gomega.BeTrue(), err.Error())
		}
	}
}

func matchTransactionRecord(tx *ttxdb.TransactionRecord, txExpected TransactionRecord, i int) error {
	if tx.TokenType != txExpected.TokenType {
		return errors.Errorf("tx [%d] tx.TokenFormat: %s, txExpected.TokenFormat: %s", i, tx.TokenType, txExpected.TokenType)
	}
	if !strings.HasPrefix(tx.SenderEID, txExpected.SenderEID) {
		return errors.Errorf("tx [%d] tx.SenderEID: %s, txExpected.SenderEID: %s", i, tx.SenderEID, txExpected.SenderEID)
	}
	if !strings.HasPrefix(tx.RecipientEID, txExpected.RecipientEID) {
		return errors.Errorf("tx [%d] tx.RecipientEID: %s, txExpected.RecipientEID: %s", i, tx.RecipientEID, txExpected.RecipientEID)
	}
	if tx.Status != txExpected.Status {
		return errors.Errorf("tx [%d] tx.Status: %d, txExpected.Status: %d", i, tx.Status, txExpected.Status)
	}
	if tx.ActionType != txExpected.ActionType {
		return errors.Errorf("tx [%d] tx.ActionType: %d, txExpected.ActionType: %d", i, tx.ActionType, txExpected.ActionType)
	}
	if tx.Amount.Cmp(txExpected.Amount) != 0 {
		return errors.Errorf("tx [%d] tx.Amount: %d, txExpected.Amount: %d", i, tx.Amount, txExpected.Amount)
	}
	if len(txExpected.PublicMetadata) > 0 && !reflect.DeepEqual(tx.PublicMetadata, txExpected.PublicMetadata) {
		return errors.Errorf("tx [%d] tx.PublicMetadata: %+v, txExpected.PublicMetadata: %+v", i, tx.PublicMetadata, txExpected.PublicMetadata)
	}

	return nil
}

func CheckBalanceAndHolding(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, auditor *token3.NodeReference) {
	CheckBalance(network, ref, wallet, typ, expected)
	CheckHolding(network, ref, wallet, typ, int64(expected), auditor) //nolint:gosec
}

func CheckBalanceAndHoldingForTMSID(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, auditor *token3.NodeReference, tmsID *token2.TMSID) {
	CheckBalanceForTMSID(network, ref, wallet, typ, expected, tmsID)
	CheckHoldingForTMSID(network, ref, wallet, typ, int64(expected), auditor, tmsID) //nolint:gosec
}

func CheckBalance(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64) {
	CheckBalanceForTMSID(network, ref, wallet, typ, expected, nil)
}

func CheckBalanceForTMSID(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, tmsID *token2.TMSID) {
	gomega.Eventually(func(g gomega.Gomega, network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, tmsID *token2.TMSID) {
		res, err := network.Client(ref.ReplicaName()).CallView("balance", common.JSONMarshall(&views.BalanceQuery{
			Wallet: wallet,
			Type:   typ,
			TMSID:  tmsID,
		}))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		b := &views.Balance{}
		common.JSONUnmarshal(res.([]byte), b)
		g.Expect(b.Type).To(gomega.BeEquivalentTo(typ))
		q, err := token.ToQuantity(b.Quantity, 64)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		expectedQ := token.NewQuantityFromUInt64(expected)
		g.Expect(expectedQ.Cmp(q)).To(gomega.BeEquivalentTo(0), "[%s]!=[%s]", expected, q)
	}).WithArguments(network, ref, wallet, typ, expected, tmsID).WithTimeout(eventualCheckTimeout).WithPolling(eventualCheckPolling).Should(gomega.Succeed())
}

func CheckPolicyOwnedBalance(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64) {
	CheckPolicyOwnedBalanceForTMSID(network, ref, wallet, typ, expected, nil)
}

func CheckPolicyOwnedBalanceForTMSID(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, tmsID *token2.TMSID) {
	res, err := network.Client(ref.ReplicaName()).CallView("PolicyOwnedBalance", common.JSONMarshall(&views.PolicyOwnedBalanceQuery{
		Wallet: wallet,
		Type:   typ,
		TMSID:  tmsID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	b := &views.Balance{}
	common.JSONUnmarshal(res.([]byte), b)
	gomega.Expect(b.Type).To(gomega.BeEquivalentTo(typ))
	q, err := token.ToQuantity(b.Quantity, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	expectedQ := token.NewQuantityFromUInt64(expected)
	gomega.Expect(expectedQ.Cmp(q)).To(gomega.BeEquivalentTo(0), "[%s]!=[%s]", expected, q)
}

func CheckCoOwnedBalance(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64) {
	CheckCoOwnedBalanceForTMSID(network, ref, wallet, typ, expected, nil)
}

func CheckCoOwnedBalanceForTMSID(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected uint64, tmsID *token2.TMSID) {
	res, err := network.Client(ref.ReplicaName()).CallView("CoOwnedBalance", common.JSONMarshall(&views.CoOwnedBalanceQuery{
		Wallet: wallet,
		Type:   typ,
		TMSID:  tmsID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	b := &views.Balance{}
	common.JSONUnmarshal(res.([]byte), b)
	gomega.Expect(b.Type).To(gomega.BeEquivalentTo(typ))
	q, err := token.ToQuantity(b.Quantity, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	expectedQ := token.NewQuantityFromUInt64(expected)
	gomega.Expect(expectedQ.Cmp(q)).To(gomega.BeEquivalentTo(0), "[%s]!=[%s]", expected, q)
}

func CheckHolding(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected int64, auditor *token3.NodeReference) {
	CheckHoldingForTMSID(network, ref, wallet, typ, expected, auditor, nil)
}

func CheckHoldingForTMSID(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected int64, auditor *token3.NodeReference, tmsID *token2.TMSID) {
	gomega.Eventually(func(g gomega.Gomega, network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token.Type, expected int64, auditor *token3.NodeReference, tmsID *token2.TMSID) {
		eIDBoxed, err := network.Client(ref.ReplicaName()).CallView("GetEnrollmentID", common.JSONMarshall(&views.GetEnrollmentID{
			Wallet: wallet,
			TMSID:  tmsID,
		}))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		eID := common.JSONUnmarshalString(eIDBoxed)
		holdingBoxed, err := network.Client(auditor.ReplicaName()).CallView("holding", common.JSONMarshall(&views.CurrentHolding{
			EnrollmentID: eID,
			TokenType:    typ,
		}))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		holding, err := strconv.Atoi(common.JSONUnmarshalString(holdingBoxed))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(holding).To(gomega.Equal(int(expected)))
	}).WithArguments(network, ref, wallet, typ, expected, auditor, tmsID).WithTimeout(eventualCheckTimeout).WithPolling(eventualCheckPolling).Should(gomega.Succeed())
}

func CheckSpending(network *integration.Infrastructure, id *token3.NodeReference, wallet string, tokenType token.Type, auditor *token3.NodeReference, expected uint64) {
	CheckSpendingForTMSID(network, id, wallet, tokenType, auditor, expected, nil)
}

func CheckSpendingForTMSID(network *integration.Infrastructure, id *token3.NodeReference, wallet string, tokenType token.Type, auditor *token3.NodeReference, expected uint64, tmsId *token2.TMSID) {
	// check spending
	// first get the enrollment id
	eIDBoxed, err := network.Client(id.ReplicaName()).CallView("GetEnrollmentID", common.JSONMarshall(&views.GetEnrollmentID{
		Wallet: wallet,
		TMSID:  tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	eID := common.JSONUnmarshalString(eIDBoxed)
	spendingBoxed, err := network.Client(auditor.ReplicaName()).CallView("spending", common.JSONMarshall(&views.CurrentSpending{
		EnrollmentID: eID,
		TokenType:    tokenType,
		TMSID:        tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	spending, err := strconv.ParseUint(common.JSONUnmarshalString(spendingBoxed), 10, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(spending).To(gomega.Equal(expected))
}

func ListIssuerHistory(network *integration.Infrastructure, wallet string, typ token.Type, issuer *token3.NodeReference) *token.IssuedTokens {
	return ListIssuerHistoryForTMSID(network, wallet, typ, issuer, nil)
}

func ListIssuerHistoryForTMSID(network *integration.Infrastructure, wallet string, typ token.Type, issuer *token3.NodeReference, tmsId *token2.TMSID) *token.IssuedTokens {
	res, err := network.Client(issuer.ReplicaName()).CallView("historyIssuedToken", common.JSONMarshall(&views.ListIssuedTokens{
		Wallet:    wallet,
		TokenType: typ,
		TMSID:     tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	issuedTokens := &token.IssuedTokens{}
	common.JSONUnmarshal(res.([]byte), issuedTokens)

	return issuedTokens
}

// CheckIssuerBalance verifies the issued, redeemed, and net balances of the given issuer
// wallet and token type on the issuer node.
func CheckIssuerBalance(network *integration.Infrastructure, issuer *token3.NodeReference, wallet string, typ token.Type, expectedIssued, expectedRedeemed, expectedNet uint64) {
	CheckIssuerBalanceForTMSID(network, issuer, wallet, typ, expectedIssued, expectedRedeemed, expectedNet, nil)
}

func CheckIssuerBalanceForTMSID(network *integration.Infrastructure, issuer *token3.NodeReference, wallet string, typ token.Type, expectedIssued, expectedRedeemed, expectedNet uint64, tmsId *token2.TMSID) {
	res, err := network.Client(issuer.ReplicaName()).CallView("issuerBalance", common.JSONMarshall(&views.IssuerBalanceQuery{
		Wallet:    wallet,
		TokenType: typ,
		TMSID:     tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	balance := &views.IssuerBalance{}
	common.JSONUnmarshal(res.([]byte), balance)

	gomega.Expect(balance.Issued).To(gomega.BeEquivalentTo(strconv.FormatUint(expectedIssued, 10)), "issued balance mismatch for [%s:%s]: got [%s], expected [%d]", wallet, typ, balance.Issued, expectedIssued)
	gomega.Expect(balance.Redeemed).To(gomega.BeEquivalentTo(strconv.FormatUint(expectedRedeemed, 10)), "redeemed balance mismatch for [%s:%s]: got [%s], expected [%d]", wallet, typ, balance.Redeemed, expectedRedeemed)
	gomega.Expect(balance.Net).To(gomega.BeEquivalentTo(strconv.FormatUint(expectedNet, 10)), "net balance mismatch for [%s:%s]: got [%s], expected [%d]", wallet, typ, balance.Net, expectedNet)
}

func ListUnspentTokens(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token.Type) *token.UnspentTokens {
	return ListUnspentTokensForTMSID(network, id, wallet, typ, nil)
}

func ListUnspentTokensForTMSID(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token.Type, tmsId *token2.TMSID) *token.UnspentTokens {
	res, err := network.Client(id.ReplicaName()).CallView("history", common.JSONMarshall(&views.ListUnspentTokens{
		Wallet:    wallet,
		TokenType: typ,
		TMSID:     tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	unspentTokens := &token.UnspentTokens{}
	common.JSONUnmarshal(res.([]byte), unspentTokens)

	return unspentTokens
}

func TransferCash(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, expectedErrorMsgs ...string) string {
	return TransferCashForTMSID(network, sender, wallet, typ, amount, receiver, auditor, nil, expectedErrorMsgs...)
}

func TransferCashForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, tmsId *token2.TMSID, expectedErrorMsgs ...string) string {
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("transfer", common.JSONMarshall(&views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amount,
		Recipient:    network.Identity(receiver.Id()),
		RecipientEID: receiver.Id(),
		TMSID:        tmsId,
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txidBoxed)
		common2.CheckFinality(network, receiver, txID, tmsId, false)
		common2.CheckFinality(network, auditor, txID, tmsId, false)

		var signers []string
		if !strings.Contains(receiver.Id(), sender.Id()) {
			signers = append(signers, strings.Split(receiver.Id(), ".")[0])
		}
		txInfo := GetTransactionInfoForTMSID(network, sender, txID, tmsId)
		for _, identity := range signers {
			sigma, ok := txInfo.EndorsementAcks[network.Identity(identity).UniqueID()]
			gomega.Expect(ok).To(gomega.BeTrue(), "identity %s not found in txInfo.EndorsementAcks", identity)
			gomega.Expect(sigma).ToNot(gomega.BeNil(), "endorsement ack sigma is nil for identity %s", identity)
		}
		gomega.Expect(len(txInfo.EndorsementAcks)).To(gomega.BeEquivalentTo(len(signers)))

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)

	return ""
}

func TransferCashNoFinalityCheck(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference) {
	_, err := network.Client(sender.ReplicaName()).CallView("transfer", common.JSONMarshall(&views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amount,
		Recipient:    network.Identity(receiver.Id()),
		RecipientEID: receiver.Id(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func TransferCashFromExternalWallet(network *integration.Infrastructure, wmp *WalletManagerProvider, websSocket bool, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, expectedErrorMsgs ...string) string {
	// obtain the recipient for the rest
	restRecipient := wmp.RecipientData(sender.Id(), wallet)
	// start the call as a stream
	var stream Stream
	var err error
	input := common.JSONMarshall(&views.Transfer{
		Auditor:                   auditor.Id(),
		Wallet:                    wallet,
		Type:                      typ,
		Amount:                    amount,
		Recipient:                 network.Identity(receiver.Id()),
		RecipientEID:              receiver.Id(),
		SenderChangeRecipientData: restRecipient,
	})
	if websSocket {
		stream, err = network.WebClient(sender.ReplicaName()).StreamCallView("transfer", input)
	} else {
		stream, err = network.Client(sender.ReplicaName()).StreamCallView("transfer", input)
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Here we handle the sign requests
	client := ttx.NewStreamExternalWalletSignerClient(wmp.SignerProvider(sender.Id(), wallet), stream, 1)
	gomega.Expect(client.Respond()).NotTo(gomega.HaveOccurred())

	// wait for the completion of the view
	txidBoxed, err := stream.Result()
	if len(expectedErrorMsgs) == 0 {
		txID := common.JSONUnmarshalString(txidBoxed)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, receiver, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)

		var signers []string
		if receiver.Id() != sender.Id() {
			signers = append(signers, receiver.Id())
		}
		txInfo := GetTransactionInfo(network, sender, txID)
		for _, identity := range signers {
			sigma, ok := txInfo.EndorsementAcks[network.Identity(identity).UniqueID()]
			gomega.Expect(ok).To(gomega.BeTrue(), "identity %s not found in txInfo.EndorsementAcks", identity)
			gomega.Expect(sigma).ToNot(gomega.BeNil(), "endorsement ack sigma is nil for identity %s", identity)
		}
		gomega.Expect(len(txInfo.EndorsementAcks)).To(gomega.BeEquivalentTo(len(signers)))

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)

	return ""
}

func TransferCashToExternalWallet(network *integration.Infrastructure, wmp *WalletManagerProvider, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, receiverWallet string, auditor *token3.NodeReference, expectedErrorMsgs ...string) string {
	// obtain the recipient data for the recipient and register it
	recipientData := wmp.RecipientData(receiver.Id(), receiverWallet)
	RegisterRecipientData(network, receiver, receiverWallet, recipientData)

	// transfer
	var err error
	input := common.JSONMarshall(&views.Transfer{
		Auditor:       auditor.Id(),
		Wallet:        wallet,
		Type:          typ,
		Amount:        amount,
		RecipientEID:  receiver.Id(),
		Recipient:     view.Identity(receiverWallet),
		RecipientData: recipientData,
	})

	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("transfer", input)
	if len(expectedErrorMsgs) == 0 {
		txID := common.JSONUnmarshalString(txidBoxed)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, receiver, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)

		var signers []string
		if receiver.Id() != sender.Id() {
			signers = append(signers, receiver.Id())
		}
		txInfo := GetTransactionInfo(network, sender, txID)
		for _, identity := range signers {
			sigma, ok := txInfo.EndorsementAcks[network.Identity(identity).UniqueID()]
			gomega.Expect(ok).To(gomega.BeTrue(), "identity [%s] not found in txInfo.EndorsementAcks [%v]", identity, txInfo.EndorsementAcks)
			gomega.Expect(sigma).ToNot(gomega.BeNil(), "endorsement ack sigma is nil for identity %s", identity)
		}
		gomega.Expect(len(txInfo.EndorsementAcks)).To(gomega.BeEquivalentTo(len(signers)))

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)

	return ""
}

func TransferCashFromAndToExternalWallet(network *integration.Infrastructure, wmp *WalletManagerProvider, websSocket bool, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, receiverWallet string, auditor *token3.NodeReference, expectedErrorMsgs ...string) string {
	// obtain the recipient for the rest
	restRecipient := wmp.RecipientData(sender.Id(), wallet)

	// obtain the recipient data for the recipient and register it
	recipientData := wmp.RecipientData(receiver.Id(), receiverWallet)
	RegisterRecipientData(network, receiver, receiverWallet, recipientData)

	// start the call as a stream
	var stream Stream
	var err error
	input := common.JSONMarshall(&views.Transfer{
		Auditor:                   auditor.Id(),
		Wallet:                    wallet,
		Type:                      typ,
		Amount:                    amount,
		RecipientEID:              receiver.Id(),
		SenderChangeRecipientData: restRecipient,
		Recipient:                 view.Identity(receiverWallet),
		RecipientData:             recipientData,
	})
	if websSocket {
		stream, err = network.WebClient(sender.ReplicaName()).StreamCallView("transfer", input)
	} else {
		stream, err = network.Client(sender.ReplicaName()).StreamCallView("transfer", input)
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Here we handle the sign requests
	client := ttx.NewStreamExternalWalletSignerClient(wmp.SignerProvider(sender.Id(), wallet), stream, 1)
	gomega.Expect(client.Respond()).NotTo(gomega.HaveOccurred())

	// wait for the completion of the view
	txidBoxed, err := stream.Result()
	if len(expectedErrorMsgs) == 0 {
		txID := common.JSONUnmarshalString(txidBoxed)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, receiver, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)

		var signers []string
		if receiver != sender {
			signers = append(signers, receiver.Id())
		}
		txInfo := GetTransactionInfo(network, sender, txID)
		for _, identity := range signers {
			sigma, ok := txInfo.EndorsementAcks[network.Identity(identity).UniqueID()]
			gomega.Expect(ok).To(gomega.BeTrue(), "identity %s not found in txInfo.EndorsementAcks", identity)
			gomega.Expect(sigma).ToNot(gomega.BeNil(), "endorsement ack sigma is nil for identity %s", identity)
		}
		gomega.Expect(len(txInfo.EndorsementAcks)).To(gomega.BeEquivalentTo(len(signers)))

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)

	return ""
}

func TransferCashMultiActions(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amounts []uint64, receivers []*token3.NodeReference, auditor *token3.NodeReference, tokenID *token.ID, expectedErrorMsgs ...string) string {
	gomega.Expect(len(amounts)).To(gomega.BeNumerically(">", 1))
	gomega.Expect(len(receivers)).To(gomega.BeEquivalentTo(len(amounts)))
	transfer := &views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amounts[0],
		Recipient:    network.Identity(receivers[0].Id()),
		RecipientEID: receivers[0].Id(),
		TokenIDs:     []*token.ID{tokenID},
	}

	uniqueKey := strconv.FormatInt(time.Now().UnixNano(), 10)
	for i := 1; i < len(amounts); i++ {
		transfer.TransferAction = append(transfer.TransferAction, views.TransferAction{
			Amount:         amounts[i],
			Recipient:      network.Identity(receivers[i].Id()),
			RecipientEID:   receivers[i].Id(),
			PublicMetadata: map[string][]byte{fmt.Sprintf("%s_%d", uniqueKey, i): fmt.Appendf(nil, "val_%d", i)},
		})
	}

	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("transfer", common.JSONMarshall(transfer))
	if len(expectedErrorMsgs) == 0 {
		txID := common.JSONUnmarshalString(txidBoxed)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var signers []string
		for _, receiver := range receivers {
			common2.CheckFinality(network, receiver, txID, nil, false)
			if receiver.Id() != sender.Id() {
				signers = append(signers, receiver.Id())
			}
		}
		common2.CheckFinality(network, auditor, txID, nil, false)

		txInfo := GetTransactionInfo(network, sender, txID)
		for _, identity := range signers {
			sigma, ok := txInfo.EndorsementAcks[network.Identity(identity).UniqueID()]
			gomega.Expect(ok).To(gomega.BeTrue(), "identity %s not found in txInfo.EndorsementAcks", identity)
			gomega.Expect(sigma).ToNot(gomega.BeNil(), "endorsement ack sigma is nil for identity %s", identity)
		}
		gomega.Expect(len(txInfo.EndorsementAcks)).To(gomega.BeEquivalentTo(len(signers)))
		params := views.ListAcceptedTransactions{SenderWallet: wallet, IDs: []string{txID}}

		txsBoxed, err := network.Client(receivers[0].Id()).CallView("acceptedTransactionHistory", common.JSONMarshall(&params))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var txs []*ttxdb.TransactionRecord
		common.JSONUnmarshal(txsBoxed.([]byte), &txs)

		gomega.Expect(len(txs)).To(gomega.BeEquivalentTo(1), "1 tx")
		gomega.Expect(len(txs[0].PublicMetadata)).To(gomega.BeEquivalentTo(1), "1 public metadata")
		gomega.Expect(txs[0].PublicMetadata[token2.PublicMetadataPrefix+uniqueKey+"_1"]).To(gomega.BeEquivalentTo([]byte("val_1")), fmt.Sprintf("public metadata = %v", txs[0].PublicMetadata))

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)
	// extract txID from err
	strErr := err.Error()
	s := strings.LastIndex(strErr, "[<<<")
	e := strings.LastIndex(strErr, ">>>]")

	return strErr[s+4 : e]
}

func MaliciousTransferCash(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, tmsId *token2.TMSID, expectedErrorMsgs ...string) string {
	txidBoxed, err := network.Client(id.ReplicaName()).CallView("MaliciousTransfer", common.JSONMarshall(&views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amount,
		Recipient:    network.Identity(receiver.Id()),
		RecipientEID: receiver.Id(),
		TMSID:        tmsId,
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txidBoxed)
		time.Sleep(5 * time.Second)

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)

	return ""
}

func PrepareTransferCash(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, tokenID *token.ID, expectedErrorMsgs ...string) (string, []byte) {
	transferInput := &views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amount,
		Recipient:    network.Identity(receiver.Id()),
		RecipientEID: receiver.Id(),
	}
	if tokenID != nil {
		transferInput.TokenIDs = []*token.ID{tokenID}
	}
	txBoxed, err := network.Client(sender.ReplicaName()).CallView("prepareTransfer", common.JSONMarshall(transferInput))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range expectedErrorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
		}
		time.Sleep(5 * time.Second)
	}
	res := &views.PrepareTransferResult{}
	common.JSONUnmarshal(txBoxed.([]byte), res)

	return res.TxID, res.TXRaw
}

func SetTransactionAuditStatus(network *integration.Infrastructure, id *token3.NodeReference, txID string, txStatus ttx.TxStatus) {
	_, err := network.Client(id.ReplicaName()).CallView("SetTransactionAuditStatus", common.JSONMarshall(views.SetTransactionAuditStatus{
		TxID:   txID,
		Status: txStatus,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func SetTransactionOwnersStatus(network *integration.Infrastructure, txID string, txStatus ttx.TxStatus, ids ...string) {
	for _, id := range ids {
		_, err := network.Client(id).CallView("SetTransactionOwnerStatus", common.JSONMarshall(views.SetTransactionOwnerStatus{
			TxID:   txID,
			Status: txStatus,
		}))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
}

func TokenSelectorUnlock(network *integration.Infrastructure, id *token3.NodeReference, txID string) {
	_, err := network.Client(id.ReplicaName()).CallView("TokenSelectorUnlock", common.JSONMarshall(views.TokenSelectorUnlock{
		TxID: txID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func BroadcastPreparedTransferCash(network *integration.Infrastructure, id *token3.NodeReference, txID string, tx []byte, finality bool, expectedErrorMsgs ...string) {
	_, err := network.Client(id.ReplicaName()).CallView("broadcastPreparedTransfer", common.JSONMarshall(&views.BroadcastPreparedTransfer{
		Tx:       tx,
		Finality: finality,
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		return
	}

	gomega.Expect(err).To(gomega.HaveOccurred(), "transaction [%s] must have been marked as invalid", txID)
	fmt.Println("Failed to broadcast ", err)
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)
}

func FinalityWithTimeout(network *integration.Infrastructure, id *token3.NodeReference, tx []byte, timeout time.Duration) {
	elapsedBoxed, err := network.Client(id.ReplicaName()).CallView("FinalityWithTimeout", common.JSONMarshall(&views.FinalityWithTimeout{
		Tx:      tx,
		Timeout: timeout,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	elapsed := JSONUnmarshalFloat64(elapsedBoxed)
	gomega.Expect(elapsed > timeout.Seconds()).To(gomega.BeTrue())
	gomega.Expect(elapsed < timeout.Seconds()*2).To(gomega.BeTrue())
}

func GetTransactionInfo(network *integration.Infrastructure, id *token3.NodeReference, txnId string) *ttx.TransactionInfo {
	return GetTransactionInfoForTMSID(network, id, txnId, nil)
}

func GetTransactionInfoForTMSID(network *integration.Infrastructure, id *token3.NodeReference, txnId string, tmsId *token2.TMSID) *ttx.TransactionInfo {
	boxed, err := network.Client(id.ReplicaName()).CallView("transactionInfo", common.JSONMarshall(&views.TransactionInfo{
		TransactionID: txnId,
		TMSID:         tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	info := &ttx.TransactionInfo{}
	common.JSONUnmarshal(boxed.([]byte), info)

	return info
}

func TransferCashByIDs(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, ids []*token.ID, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, failToRelease bool, expectedErrorMsgs ...string) string {
	txIDBoxed, err := network.Client(ref.ReplicaName()).CallView("transfer", common.JSONMarshall(&views.Transfer{
		Auditor:       auditor.Id(),
		Wallet:        wallet,
		Type:          "",
		TokenIDs:      ids,
		Amount:        amount,
		Recipient:     network.Identity(receiver.Id()),
		RecipientEID:  receiver.Id(),
		FailToRelease: failToRelease,
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txIDBoxed)
		common2.CheckFinality(network, receiver, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)

		return txID
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range expectedErrorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
		}
		time.Sleep(5 * time.Second)

		return ""
	}
}

func TransferCashWithSelector(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, expectedErrorMsgs ...string) {
	txIDBoxed, err := network.Client(sender.ReplicaName()).CallView("transferWithSelector", common.JSONMarshall(&views.Transfer{
		Auditor:      auditor.Id(),
		Wallet:       wallet,
		Type:         typ,
		Amount:       amount,
		Recipient:    network.Identity(receiver.Id()),
		RecipientEID: receiver.Id(),
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txIDBoxed)
		common2.CheckFinality(network, receiver, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range expectedErrorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
		}
		time.Sleep(5 * time.Second)
	}
}

func RedeemCashForTMSID(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token.Type, amount uint64, auditor *token3.NodeReference, issuer *token3.NodeReference, tmsID *token2.TMSID, expectedErrorMsgs ...string) {
	issuerName := ""
	var issuerPublicParamsPublicKey view.Identity = nil
	if issuer != nil && tmsID != nil {
		issuerName = issuer.Id()
		tms := GetTMSByTMSID(network, *tmsID)
		issuerPublicParamsPublicKey = GetIssuerIdentity(tms, issuer.Id())
	}

	txID, err := network.Client(id.ReplicaName()).CallView("redeem", common.JSONMarshall(&views.Redeem{
		Auditor:                     auditor.Id(),
		Issuer:                      issuerName,
		IssuerPublicParamsPublicKey: issuerPublicParamsPublicKey,
		Wallet:                      wallet,
		Type:                        typ,
		Amount:                      amount,
		TMSID:                       tmsID,
	}))
	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, auditor, common.JSONUnmarshalString(txID), tmsID, false)
		common2.CheckFinality(network, issuer, common.JSONUnmarshalString(txID), tmsID, false)

		return
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}
	time.Sleep(5 * time.Second)
}

// AuthorizeRedeemIssuer adds the passed issuer identity to the auditor's allow-list of issuers
// authorized to approve redeem operations.
func AuthorizeRedeemIssuer(network *integration.Infrastructure, auditor *token3.NodeReference, issuerIdentity []byte) {
	_, err := network.Client(auditor.ReplicaName()).CallView("AuthorizeRedeemIssuer", common.JSONMarshall(&views.AuthorizeRedeemIssuer{
		Issuer: issuerIdentity,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func RedeemCashByIDs(network *integration.Infrastructure, networkName string, id *token3.NodeReference, wallet string, ids []*token.ID, amount uint64, auditor *token3.NodeReference, issuer *token3.NodeReference) {
	issuerName := ""
	var issuerSigningKey view.Identity = nil
	if issuer != nil {
		issuerName = issuer.Id()
		tms := GetTMSByNetworkName(network, networkName)
		issuerSigningKey = GetIssuerIdentity(tms, issuer.Id())
	}

	txid, err := network.Client(id.ReplicaName()).CallView("redeem", common.JSONMarshall(&views.Redeem{
		Auditor:                     auditor.Id(),
		Issuer:                      issuerName,
		IssuerPublicParamsPublicKey: issuerSigningKey,
		Wallet:                      wallet,
		Type:                        "",
		TokenIDs:                    ids,
		Amount:                      amount,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	common2.CheckFinality(network, auditor, common.JSONUnmarshalString(txid), nil, false)
}

func SwapCash(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typeLeft token.Type, amountLeft uint64, typRight token.Type, amountRight uint64, receiver *token3.NodeReference, auditor *token3.NodeReference) {
	txid, err := network.Client(id.ReplicaName()).CallView("swap", common.JSONMarshall(&views.Swap{
		Auditor:         auditor.Id(),
		AliceWallet:     wallet,
		FromAliceType:   typeLeft,
		FromAliceAmount: amountLeft,
		FromBobType:     typRight,
		FromBobAmount:   amountRight,
		Bob:             network.Identity(receiver.Id()),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txid)
	common2.CheckFinality(network, id, txID, nil, false)
	common2.CheckFinality(network, receiver, txID, nil, false)
	common2.CheckFinality(network, auditor, txID, nil, false)
}

func CheckPublicParams(network *integration.Infrastructure, ids ...*token3.NodeReference) {
	CheckPublicParamsForTMSID(network, nil, ids...)
}

func GetTXStatus(network *integration.Infrastructure, id *token3.NodeReference, txID string) *views.TxStatusResponse {
	boxed, err := network.Client(id.ReplicaName()).CallView("TxStatus", common.JSONMarshall(&views.TxStatus{
		TMSID: token2.TMSID{},
		TxID:  txID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to check public params at [%s]", id)
	response := &views.TxStatusResponse{}
	common.JSONUnmarshal(boxed.([]byte), response)

	return response
}

func CheckPublicParamsForTMSID(network *integration.Infrastructure, tmsId *token2.TMSID, ids ...*token3.NodeReference) {
	var errs []error
	for _, id := range ids {
		for _, replicaName := range id.AllNames() {
			if network.Client(replicaName) == nil {
				panic("did not find id " + replicaName)
			}

			for range eventualCheckTimeout / time.Second {
				_, err := network.Client(replicaName).CallView("CheckPublicParamsMatch", common.JSONMarshall(&views.CheckPublicParamsMatch{
					TMSID: tmsId,
				}))
				if err == nil {
					break
				}
				errs = append(errs, errors.WithMessagef(err, "failed to check public params at [%s]", id))
				time.Sleep(eventualCheckPolling)
			}

			// gomega.Eventually(func(g gomega.Gomega, replicaName string, tmsId *token2.TMSID, id *token3.NodeReference) {
			// 	_, err := network.Client(replicaName).CallView("CheckPublicParamsMatch", common.JSONMarshall(&views.CheckPublicParamsMatch{
			// 		TMSID: tmsId,
			// 	}))
			// 	g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to check public params at [%s]", id)
			// }).WithArguments(replicaName, tmsId, id).WithTimeout(eventualCheckTimeout).WithPolling(eventualCheckPolling).Should(gomega.Succeed())
		}
	}
	if len(errs) != 0 {
		gomega.Expect(errors2.Join(errs...)).ToNot(gomega.HaveOccurred(), "failed to check public params")
	}
}

func CheckPublicParamsMatch(network *integration.Infrastructure, tmsId *token2.TMSID, ids ...*token3.NodeReference) bool {
	for _, id := range ids {
		for _, replicaName := range id.AllNames() {
			_, err := network.Client(replicaName).CallView("CheckPublicParamsMatch", common.JSONMarshall(&views.CheckPublicParamsMatch{
				TMSID: tmsId,
			}))
			if err != nil {
				return false
			}
		}
	}

	return true
}

func GetTMSByNetworkName(network *integration.Infrastructure, networkName string) *topology.TMS {
	tp := tplatform.GetPlatform(network.NWOCtx, "token")
	gomega.Expect(tp).NotTo(gomega.BeNil())
	for _, TMS := range tp.GetTopology().TMSs {
		if TMS.Network == networkName {
			return TMS
		}
	}
	panic(fmt.Sprintf("TMS not found for network [%s]", networkName))
}

func GetTMSByTMSID(network *integration.Infrastructure, tmsID token2.TMSID) *topology.TMS {
	tp := tplatform.GetPlatform(network.NWOCtx, "token")
	gomega.Expect(tp).NotTo(gomega.BeNil())
	for _, tms := range tp.GetTopology().TMSs {
		if tms.Network == tmsID.Network && tms.Channel == tmsID.Channel && tms.Namespace == tmsID.Namespace {
			return tms
		}
	}
	panic(fmt.Sprintf("TMS not found for id [%s]", tmsID))
}

func GetTMSByAlias(network *integration.Infrastructure, alias topology.TMSAlias) *topology.TMS {
	tp := tplatform.GetPlatform(network.NWOCtx, "token")
	gomega.Expect(tp).NotTo(gomega.BeNil())
	for _, TMS := range tp.GetTopology().TMSs {
		if TMS.Alias == alias {
			return TMS
		}
	}
	panic(fmt.Sprintf("TMS not found for alias [%s]", alias))
}

func UpdatePublicParams(network *integration.Infrastructure, publicParams []byte, tms *topology.TMS) {
	p := network.NWOCtx.PlatformsByName["token"]
	p.(*tplatform.Platform).UpdatePublicParams(tms, publicParams)
}

func UpdatePublicParamsAndWait(network *integration.Infrastructure, publicParams []byte, tms *topology.TMS, nodes ...*token3.NodeReference) {
	p := network.NWOCtx.PlatformsByName["token"]
	p.(*tplatform.Platform).UpdatePublicParams(tms, publicParams)
	for _, node := range nodes {
		if node.Id() == "custodian" {
			continue
		}
		gomega.Eventually(GetPublicParams).WithArguments(network, node).WithTimeout(60 * time.Second).WithPolling(5 * time.Second).Should(gomega.Equal(publicParams))
	}
}

func GetPublicParams(network *integration.Infrastructure, id *token3.NodeReference) []byte {
	pp, err := network.Client(id.ReplicaName()).CallView("GetPublicParams", common.JSONMarshall(&views.GetPublicParams{}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return pp.([]byte)
}

func DoesWalletExist(network *integration.Infrastructure, id *token3.NodeReference, wallet string, walletType int) bool {
	boxed, err := network.Client(id.ReplicaName()).CallView("DoesWalletExist", common.JSONMarshall(&views.DoesWalletExist{
		Wallet:     wallet,
		WalletType: walletType,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var exists bool
	switch v := boxed.(type) {
	case []byte:
		err := json.Unmarshal(v, &exists)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	case string:
		err := json.Unmarshal([]byte(v), &exists)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	default:
		panic(fmt.Sprintf("type not recognized [%T]", v))
	}

	return exists
}

func CheckOwnerStore(network *integration.Infrastructure, errorCheck func([]string) error, ids ...*token3.NodeReference) {
	for _, id := range ids {
		for _, replicaName := range id.AllNames() {
			errorMessagesBoxed, err := network.Client(replicaName).CallView("CheckTTXDB", common.JSONMarshall(&views.CheckTTXDB{}))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			var errorMessages []string
			common.JSONUnmarshal(errorMessagesBoxed.([]byte), &errorMessages)
			if errorCheck != nil {
				gomega.Expect(errorCheck(errorMessages)).NotTo(gomega.HaveOccurred(), "failed to check errors")
			} else {
				gomega.Expect(errorMessages).To(gomega.BeEmpty(), "expected zero errors, got [%v]", errorMessages)
			}
		}
	}
}

func CheckAuditorStore(network *integration.Infrastructure, auditor *token3.NodeReference, walletID string, errorCheck func([]string) error) {
	errorMessagesBoxed, err := network.Client(auditor.ReplicaName()).CallView("CheckTTXDB", common.JSONMarshall(&views.CheckTTXDB{
		Auditor:         true,
		AuditorWalletID: walletID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var errorMessages []string
	common.JSONUnmarshal(errorMessagesBoxed.([]byte), &errorMessages)
	if errorCheck != nil {
		gomega.Expect(errorCheck(errorMessages)).NotTo(gomega.HaveOccurred(), "failed to check errors")
	} else {
		gomega.Expect(errorMessages).To(gomega.BeEmpty(), "expected zero errors, got [%v]", errorMessages)
	}
}

func PruneInvalidUnspentTokens(network *integration.Infrastructure, ids ...*token3.NodeReference) {
	for _, id := range ids {
		eIDBoxed, err := network.Client(id.ReplicaName()).CallView("PruneInvalidUnspentTokens", common.JSONMarshall(&views.PruneInvalidUnspentTokens{}))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var deleted []*token.ID
		common.JSONUnmarshal(eIDBoxed.([]byte), &deleted)
		gomega.Expect(deleted).To(gomega.BeEmpty(), "expected 0 tokens to be deleted at [%s], got [%d]", id, len(deleted))
	}
}

func ListVaultUnspentTokens(network *integration.Infrastructure, id *token3.NodeReference) []*token.ID {
	res, err := network.Client(id.ReplicaName()).CallView("ListVaultUnspentTokens", common.JSONMarshall(&views.ListVaultUnspentTokens{}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	unspentTokens := &token.UnspentTokens{}
	common.JSONUnmarshal(res.([]byte), unspentTokens)
	count := unspentTokens.Count()
	var IDs []*token.ID
	for i := range count {
		tok := unspentTokens.At(i)
		IDs = append(IDs, &tok.Id)
	}

	return IDs
}

func CheckIfExistsInVault(network *integration.Infrastructure, id *token3.NodeReference, tokenIDs []*token.ID) {
	_, err := network.Client(id.ReplicaName()).CallView("CheckIfExistsInVault", common.JSONMarshall(&views.CheckIfExistsInVault{IDs: tokenIDs}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func WhoDeletedToken(network *integration.Infrastructure, id *token3.NodeReference, tokenIDs []*token.ID, txIDs ...string) *views.WhoDeletedTokenResult {
	boxed, err := network.Client(id.ReplicaName()).CallView("WhoDeletedToken", common.JSONMarshall(&views.WhoDeletedToken{
		TokenIDs: tokenIDs,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var result *views.WhoDeletedTokenResult
	common.JSONUnmarshal(boxed.([]byte), &result)
	gomega.Expect(len(result.Deleted)).To(gomega.BeEquivalentTo(len(tokenIDs)))
	for i, txID := range txIDs {
		gomega.Expect(result.Deleted[i]).To(gomega.BeTrue(), "expected token [%s] to be deleted", tokenIDs[i])
		gomega.Expect(result.Who[i]).To(gomega.BeEquivalentTo(txID))
	}

	return result
}

func GetAuditorIdentity(tms *topology.TMS, id string) []byte {
	return getIdentity(tms.Wallets.Auditors, id)
}

func GetIssuerIdentity(tms *topology.TMS, id string) []byte {
	return getIdentity(tms.Wallets.Issuers, id)
}

func getIdentity(identities []topology.Identity, id string) []byte {
	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	for _, topologyIdentity := range identities {
		if topologyIdentity.ID == id {
			// Build an MSP Identity
			kmp, _, err := x509.NewKeyManager(topologyIdentity.Path, topologyIdentity.Opts, keyStore)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			identityDescriptor, err := kmp.Identity(context.Background(), nil)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			return wrap
		}
	}
	gomega.Expect(false).To(gomega.BeTrue(), "identity not found in [%s]", id)

	return nil
}

func JSONUnmarshalFloat64(v any) float64 {
	var s float64
	switch v := v.(type) {
	case []byte:
		err := json.Unmarshal(v, &s)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	case string:
		err := json.Unmarshal([]byte(v), &s)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	default:
		panic(fmt.Sprintf("type not recognized [%T]", v))
	}

	return s
}

type deleteVaultPlatform interface {
	DeleteVault(id string)
}

func Restart(network *integration.Infrastructure, deleteVault bool, onRestart OnRestartFunc, ids ...*token3.NodeReference) {
	logger.Infof("restart [%v], [%v]", ids, RestartEnabled)
	if !RestartEnabled {
		return
	}
	for _, id := range ids {
		network.StopFSCNode(id.Id())
	}
	time.Sleep(10 * time.Second)
	if deleteVault {
		for _, id := range ids {
			for name, platform := range network.NWOCtx.PlatformsByName {
				if dv, ok := platform.(deleteVaultPlatform); ok {
					logger.Infof("Platform %d supports delete vault. Deleting...", name)
					dv.DeleteVault(id.Id())
				}
			}

			// delete token dbs as well
			tokenPlatform := tplatform.GetPlatform(network.NWOCtx, "token")
			gomega.Expect(tokenPlatform).ToNot(gomega.BeNil(), "cannot find token platform in context")
			for _, tms := range tokenPlatform.GetTopology().TMSs {
				tokenPlatform.DeleteDBs(tms, id.Id())
			}
		}
	}
	for _, id := range ids {
		network.StartFSCNode(id.Id())
	}
	time.Sleep(10 * time.Second)
	if deleteVault {
		// Add extra time to wait for the vault to be reconstructed
		time.Sleep(40 * time.Second)
	}
	if onRestart != nil {
		for _, id := range ids {
			logger.Infof("Calling on restart for [%s]", id.Id())
			onRestart(network, id.Id())
		}
	}
}

func CopyDBsTo(network *integration.Infrastructure, to string, ids ...*token3.NodeReference) {
	tokenPlatform := tplatform.GetPlatform(network.NWOCtx, "token")
	gomega.Expect(tokenPlatform).ToNot(gomega.BeNil(), "cannot find token platform in context")

	for _, id := range ids {
		for _, tms := range tokenPlatform.GetTopology().TMSs {
			tokenPlatform.CopyDBsTo(tms, id.Id(), filepath.Join(to, tms.ID(), id.Id()))
		}
	}
}

func RegisterIssuerIdentity(network *integration.Infrastructure, id *token3.NodeReference, walletID, walletPath string) {
	_, err := network.Client(id.ReplicaName()).CallView("RegisterIssuerIdentity", common.JSONMarshall(&views.RegisterIssuerWallet{
		ID:   walletID,
		Path: walletPath,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	network.NWOCtx.SetViewClient(walletPath, network.Client(id.ReplicaName()))
}

func RegisterOwnerIdentity(ctx context.Context, network *integration.Infrastructure, id *token3.NodeReference, identityConfiguration token2.IdentityConfiguration) {
	for _, replicaName := range id.AllNames() { // TODO: AF
		_, err := network.Client(replicaName).CallView("RegisterOwnerIdentity", common.JSONMarshall(&views.RegisterOwnerIdentity{
			IdentityConfiguration: identityConfiguration,
		}))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "cannot register owner identity at replica [%s]", replicaName)
		network.NWOCtx.SetViewClient(identityConfiguration.ID, network.Client(replicaName))
	}
}

func RegisterRecipientData(network *integration.Infrastructure, ref *token3.NodeReference, walletID string, rd *token2.RecipientData) {
	_, err := network.Client(ref.ReplicaName()).CallView("RegisterRecipientData", common.JSONMarshall(&views.RegisterRecipientData{
		WalletID:      walletID,
		RecipientData: *rd,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	network.NWOCtx.SetViewClient(walletID, network.Client(ref.ReplicaName()))
}

func CheckOwnerWalletIDs(network *integration.Infrastructure, owner *token3.NodeReference, ids ...string) {
	idsBoxed, err := network.Client(owner.ReplicaName()).CallView("ListOwnerWalletIDsView", nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var wIDs []string
	common.JSONUnmarshal(idsBoxed.([]byte), &wIDs)
	for _, wID := range ids {
		found := slices.Contains(wIDs, wID)
		gomega.Expect(found).To(gomega.BeTrue(), "[%s] is not in [%v]", wID, wIDs)
	}
}

func RevokeIdentity(network *integration.Infrastructure, auditor *token3.NodeReference, rh string) {
	_, err := network.Client(auditor.ReplicaName()).CallView("RevokeUser", common.JSONMarshall(&views.RevokeUser{
		RH: rh,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func GetRevocationHandle(network *integration.Infrastructure, ref *token3.NodeReference) string {
	rhBoxed, err := network.Client(ref.ReplicaName()).CallView("GetRevocationHandle", common.JSONMarshall(&views.GetRevocationHandle{}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	rh := &views.RevocationHandle{}
	common.JSONUnmarshal(rhBoxed.([]byte), rh)
	fmt.Printf("GetRevocationHandle [%s][%s]", rh.RH, fsclogging.SHA256Base64([]byte(rh.RH)))

	return rh.RH
}

func SetKVSEntry(network *integration.Infrastructure, user *token3.NodeReference, key string, value string) {
	_, err := network.Client(user.ReplicaName()).CallView("SetKVSEntry", common.JSONMarshall(&views.KVSEntry{
		Key:   key,
		Value: value,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func SetSpendableFlag(network *integration.Infrastructure, user *token3.NodeReference, tokenID token.ID, value bool) {
	_, err := network.Client(user.ReplicaName()).CallView("SetSpendableFlag", common.JSONMarshall(&views.SetSpendableFlag{
		TMSID:     token2.TMSID{},
		TokenID:   tokenID,
		Spendable: value,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func Withdraw(network *integration.Infrastructure, wpm *WalletManagerProvider, webSocket bool, user *token3.NodeReference, wallet string, typ token.Type, amount uint64, auditor *token3.NodeReference, issuer *token3.NodeReference, expectedErrorMsgs ...string) string {
	var recipientData *token2.RecipientData
	if wpm != nil {
		recipientData = wpm.RecipientData(user.Id(), wallet)
	}
	input := common.JSONMarshall(&views.Withdrawal{
		Wallet:        wallet,
		TokenType:     typ,
		Amount:        amount,
		Issuer:        issuer.Id(),
		RecipientData: recipientData,
	})
	var stream Stream
	var txIDBoxed any
	var err error
	if wpm != nil {
		if webSocket {
			stream, err = network.WebClient(user.ReplicaName()).StreamCallView("withdrawal", input)
		} else {
			stream, err = network.Client(user.ReplicaName()).StreamCallView("withdrawal", input)
		}
	} else {
		txIDBoxed, err = network.Client(user.ReplicaName()).CallView("withdrawal", input)
	}

	// Here we handle the sign requests
	if wpm != nil {
		client := ttx.NewStreamExternalWalletSignerClient(wpm.SignerProvider(user.Id(), wallet), stream, 1)
		gomega.Expect(client.Respond()).NotTo(gomega.HaveOccurred())
	}

	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		if wpm != nil {
			txIDBoxed, err = stream.Result()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
		txID := common.JSONUnmarshalString(txIDBoxed)
		common2.CheckFinality(network, user, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)
		common2.CheckFinality(network, issuer, txID, nil, false)

		return txID
	}

	gomega.Expect(txIDBoxed).To(gomega.BeEmpty())
	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}

	return ""
}

func DisableRestart() {
	RestartEnabled = false
}

func CheckLocalMetrics(ii *integration.Infrastructure, user string, viewName string) {
	metrics, err := ii.WebClient(user).Metrics()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(metrics).NotTo(gomega.BeEmpty())

	var sum float64
	for _, m := range metrics[prometheus.ViewCallsOperationsMetric].GetMetric() {
		for _, labelPair := range m.Label {
			if labelPair.GetName() == "view" && labelPair.GetValue() == viewName {
				sum += m.Counter.GetValue()
			}
		}
	}

	logger.Infof(
		"Received in total %f view operations for [%s] for user %s: %v",
		sum,
		viewName,
		user,
		metrics[prometheus.ViewCallsOperationsMetric].GetMetric(),
	)
	gomega.Expect(sum).NotTo(gomega.BeZero())
}

func CheckPrometheusMetrics(ii *integration.Infrastructure, viewName string) {
	cli, err := ii.NWO.PrometheusReporter()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	vector, err := cli.GetVector(model.Metric{
		"__name__": prometheus.ViewCallsOperationsMetric,
		"view":     model.LabelValue(viewName),
	})
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(vector).NotTo(gomega.BeEmpty())
	for _, v := range vector {
		gomega.Expect(v.Value).NotTo(gomega.Equal(model.SampleValue(0)))
	}
}

func TokensUpgrade(network *integration.Infrastructure, wpm *WalletManagerProvider, user *token3.NodeReference, wallet string, typ token.Type, auditor *token3.NodeReference, issuer *token3.NodeReference, expectedErrorMsgs ...string) string {
	var recipientData *token2.RecipientData
	if wpm != nil {
		recipientData = wpm.RecipientData(user.Id(), wallet)
	}
	txid, err := network.Client(user.ReplicaName()).CallView("TokensUpgrade", common.JSONMarshall(&views.TokensUpgrade{
		Wallet:        wallet,
		TokenType:     typ,
		Issuer:        issuer.Id(),
		RecipientData: recipientData,
	}))

	if len(expectedErrorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txid)
		fmt.Printf("TokensUpgrade txID [%s]\n", txID)
		common2.CheckFinality(network, user, txID, nil, false)
		common2.CheckFinality(network, auditor, txID, nil, false)
		common2.CheckFinality(network, issuer, txID, nil, false)

		return txID
	}

	gomega.Expect(err).To(gomega.HaveOccurred())
	for _, msg := range expectedErrorMsgs {
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg), "err [%s] should contain [%s]", err.Error(), msg)
	}

	return ""
}

func MultiSigLockCash(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receivers []*token3.NodeReference, auditor *token3.NodeReference) string {
	return MultiSigLockCashForTMSID(network, sender, wallet, typ, amount, receivers, auditor, nil)
}

func MultiSigLockCashForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, receivers []*token3.NodeReference, auditor *token3.NodeReference, tmsId *token2.TMSID) string {
	identities := make([]view.Identity, len(receivers))
	eids := make([]string, len(receivers))
	for i := range receivers {
		eids[i] = receivers[i].Id()
		identities[i] = network.Identity(eids[i])
	}
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("MultiSigLock", common.JSONMarshall(&views.MultiSigLock{
		Auditor:            auditor.Id(),
		Wallet:             wallet,
		Type:               typ,
		Amount:             amount,
		MultisigIdentities: identities,
		MultisigEIDs:       eids,
		TMSID:              tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txidBoxed)

	return txID
}

func MultiSigSpendCash(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference) string {
	return MultiSigSpendCashForTMSID(network, sender, wallet, typ, receiver, auditor, nil)
}

func MultiSigSpendCashForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference, tmsId *token2.TMSID) string {
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("MultiSigSpend", common.JSONMarshall(&views.MultiSigSpend{
		Auditor:   auditor.Id(),
		Wallet:    wallet,
		TMSID:     tmsId,
		Recipient: network.Identity(receiver.Id()),
		TokenType: typ,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txidBoxed)

	return txID
}

// PolicyLockCash locks amount tokens of the given type into a policy identity
// composed of the given receivers and governed by the boolean policy expression.
func PolicyLockCash(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, policy string, receivers []*token3.NodeReference, auditor *token3.NodeReference) string {
	return PolicyLockCashForTMSID(network, sender, wallet, typ, amount, policy, receivers, auditor, nil)
}

// PolicyLockCashForTMSID is like PolicyLockCash but pins a specific TMSID.
func PolicyLockCashForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, amount uint64, policy string, receivers []*token3.NodeReference, auditor *token3.NodeReference, tmsID *token2.TMSID) string {
	parties := make([]view.Identity, len(receivers))
	for i, r := range receivers {
		parties[i] = network.Identity(r.Id())
	}
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("PolicyLock", common.JSONMarshall(&views.PolicyLock{
		Auditor:       auditor.Id(),
		Wallet:        wallet,
		Type:          typ,
		Amount:        amount,
		Policy:        policy,
		PolicyParties: parties,
		TMSID:         tmsID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return common.JSONUnmarshalString(txidBoxed)
}

// PolicySpendCashOR spends a policy token using only the sender as the signing
// component identity (OR-policy optimisation: no co-owner coordination needed).
func PolicySpendCashOR(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference) string {
	return PolicySpendCashORForTMSID(network, sender, wallet, typ, receiver, auditor, nil)
}

// PolicySpendCashORForTMSID is like PolicySpendCashOR but pins a specific TMSID.
func PolicySpendCashORForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference, tmsID *token2.TMSID) string {
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("PolicySpend", common.JSONMarshall(&views.PolicySpend{
		Auditor:   auditor.Id(),
		Wallet:    wallet,
		TMSID:     tmsID,
		Recipient: network.Identity(receiver.Id()),
		TokenType: typ,
		Signers:   []view.Identity{network.Identity(sender.Id())},
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return common.JSONUnmarshalString(txidBoxed)
}

// PolicySpendCashAND spends a policy token that requires all co-owners to sign,
// coordinating with them via RequestSpendView before collecting endorsements.
func PolicySpendCashAND(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference) string {
	return PolicySpendCashANDForTMSID(network, sender, wallet, typ, receiver, auditor, nil)
}

// PolicySpendCashANDForTMSID is like PolicySpendCashAND but pins a specific TMSID.
func PolicySpendCashANDForTMSID(network *integration.Infrastructure, sender *token3.NodeReference, wallet string, typ token.Type, receiver *token3.NodeReference, auditor *token3.NodeReference, tmsID *token2.TMSID) string {
	txidBoxed, err := network.Client(sender.ReplicaName()).CallView("PolicySpend", common.JSONMarshall(&views.PolicySpend{
		Auditor:   auditor.Id(),
		Wallet:    wallet,
		TMSID:     tmsID,
		Recipient: network.Identity(receiver.Id()),
		TokenType: typ,
		// Signers is nil: all co-owners are contacted and must sign.
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return common.JSONUnmarshalString(txidBoxed)
}

func BindIssuerNetworkAndSigningIdentities(network *integration.Infrastructure, issuer *token3.NodeReference, issuerPublicKey []byte, onNodes ...*token3.NodeReference) {
	for _, node := range onNodes {
		for _, nodeReplica := range node.AllNames() {
			for _, issuerName := range issuer.AllNames() {
				_, err := network.Client(nodeReplica).CallView("SetBinding", common.JSONMarshall(&views.Binding{
					FSCNodeIdentity: network.Identity(issuerName), // issuer's network node identity.
					Alias:           issuerPublicKey,              // issuer's public key for the token issuance
				}))

				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			}
		}
	}
}
func PrepareUpdatedPublicParams(network *integration.Infrastructure, auditor string, issuer string, networkName string, appendIdentities bool) []byte {
	tms := GetTMSByNetworkName(network, networkName)
	auditorId := GetAuditorIdentity(tms, auditor)
	issuerId := GetIssuerIdentity(tms, issuer)

	tokenPlatform, ok := network.NWOCtx.PlatformsByName["token"].(*tplatform.Platform)
	gomega.Expect(ok).To(gomega.BeTrue(), "failed to get token platform from context")

	// Deserialize current params
	ppBytes, err := os.ReadFile(tokenPlatform.PublicParametersFile(tms))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	genericPP, err := pp.Unmarshal(ppBytes)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	type PP interface {
		Validate() error
		Serialize() ([]byte, error)
		SetIssuers(identities []driver.Identity)
		SetAuditors(identities []driver.Identity)
		AddAuditor(identity2 driver.Identity)
		AddIssuer(identity2 driver.Identity)
	}
	var pp PP
	switch genericPP.Identifier {
	case zkatdlognoghv1.DriverIdentifier:
		pp, err = dlognoghv1.NewPublicParamsFromBytes(ppBytes, dlognoghv1.DLogNoGHDriverName, dlognoghv1.ProtocolV1)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	case gfabtokenv1.DriverIdentifier:
		pp, err = fabtokenv1.NewPublicParamsFromBytes(ppBytes, fabtokenv1.FabTokenDriverName, fabtokenv1.ProtocolV1)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	default:
		gomega.Expect(false).To(gomega.BeTrue(), "unknown pp identifier [%s]", genericPP.Identifier)
	}

	gomega.Expect(pp.Validate()).NotTo(gomega.HaveOccurred())
	if appendIdentities {
		pp.AddAuditor(auditorId)
		pp.AddIssuer(issuerId)
	} else {
		pp.SetAuditors([]driver.Identity{auditorId})
		pp.SetIssuers([]driver.Identity{issuerId})
	}

	// Serialize
	ppBytes, err = pp.Serialize()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return ppBytes
}

func PreparePublicParamsWithNewIssuer(network *integration.Infrastructure, issuerWalletPath string, networkName string) []byte {
	tms := GetTMSByNetworkName(network, networkName)
	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	kmp, _, err := x509.NewKeyManager(issuerWalletPath, nil, keyStore)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	identityDescriptor, err := kmp.Identity(context.Background(), nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	tokenPlatform, ok := network.NWOCtx.PlatformsByName["token"].(*tplatform.Platform)
	gomega.Expect(ok).To(gomega.BeTrue(), "failed to get token platform from context")

	// Deserialize current params
	ppBytes, err := os.ReadFile(tokenPlatform.PublicParametersFile(tms))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	genericPP, err := pp.Unmarshal(ppBytes)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	type PP interface {
		AddIssuer(id driver.Identity)
		Validate() error
		Serialize() ([]byte, error)
	}
	var pp PP
	switch genericPP.Identifier {
	case zkatdlognoghv1.DriverIdentifier:
		pp, err = dlognoghv1.NewPublicParamsFromBytes(ppBytes, dlognoghv1.DLogNoGHDriverName, dlognoghv1.ProtocolV1)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	case gfabtokenv1.DriverIdentifier:
		pp, err = fabtokenv1.NewPublicParamsFromBytes(ppBytes, fabtokenv1.FabTokenDriverName, fabtokenv1.ProtocolV1)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	default:
		gomega.Expect(false).To(gomega.BeTrue(), "unknown pp identitfier [%s]", genericPP.Identifier)
	}

	// validate public params
	gomega.Expect(pp.Validate()).NotTo(gomega.HaveOccurred())
	// Update them
	pp.AddIssuer(wrap)

	// Serialize
	ppBytes, err = pp.Serialize()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return ppBytes
}
