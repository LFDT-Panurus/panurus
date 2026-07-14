/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dvp

import (
	"encoding/json"
	"time"

	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	views2 "github.com/LFDT-Panurus/panurus/integration/token/dvp/views"
	"github.com/LFDT-Panurus/panurus/integration/token/dvp/views/cash"
	"github.com/LFDT-Panurus/panurus/integration/token/dvp/views/house"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/onsi/gomega"
)

const (
	USD = token2.Type("USD")
)

func TestAll(network *integration.Infrastructure, sel *token3.ReplicaSelector) {
	cashIssuer := sel.Get("cash_issuer")
	buyer := sel.Get("buyer")
	seller := sel.Get("seller")
	houseIssuer := sel.Get("house_issuer")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	// Ready to go
	registerAuditor(network)
	issueCash(network, cashIssuer, buyer)
	checkBalance(network, buyer, "", USD, 10)
	checkBalance(network, seller, "", USD, 0)
	houseID := issueHouse(network, 4, houseIssuer, seller)
	queryHouse(network, seller, houseID, "5th Avenue")
	queryHouse(network, buyer, houseID, "5th Avenue", "failed loading house with id")
	sellHouse(network, houseID, seller, buyer)
	queryHouse(network, buyer, houseID, "5th Avenue")
	queryHouse(network, seller, houseID, "5th Avenue", "failed loading house with id")
	checkBalance(network, buyer, "", USD, 6)
	checkBalance(network, seller, "", USD, 4)
}

func registerAuditor(network *integration.Infrastructure) {
	_, err := network.Client("auditor").CallView("registerAuditor", nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func issueCash(network *integration.Infrastructure, issuer *token3.NodeReference, buyer *token3.NodeReference) {
	_, err := network.Client(issuer.ReplicaName()).CallView("issue_cash", common.JSONMarshall(cash.IssueCash{
		IssuerWallet: "",
		TokenType:    USD,
		Quantity:     10,
		Recipient:    buyer.Id(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	time.Sleep(5 * time.Second)
}

func issueHouse(network *integration.Infrastructure, valuation uint64, issuer *token3.NodeReference, seller *token3.NodeReference) string {
	houseIDBoxed, err := network.Client(issuer.ReplicaName()).CallView("issue_house", common.JSONMarshall(house.IssueHouse{
		IssuerWallet: "",
		Recipient:    seller.Id(),
		Address:      "5th Avenue",
		Valuation:    valuation,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	time.Sleep(5 * time.Second)

	return common.JSONUnmarshalString(houseIDBoxed)
}

func sellHouse(network *integration.Infrastructure, houseID string, seller *token3.NodeReference, buyer *token3.NodeReference) {
	txIDBoxed, err := network.Client(seller.ReplicaName()).CallView("sell", common.JSONMarshall(views2.Sell{
		HouseID: houseID,
		Buyer:   buyer.Id(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	common2.CheckFinality(network, buyer, common.JSONUnmarshalString(txIDBoxed), nil, false)
}

func checkBalance(network *integration.Infrastructure, ref *token3.NodeReference, wallet string, typ token2.Type, expected uint64) {
	res, err := network.Client(ref.ReplicaName()).CallView("balance", common.JSONMarshall(&views2.BalanceQuery{
		Wallet: wallet,
		Type:   typ,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	b := &views2.Balance{}
	common.JSONUnmarshal(res.([]byte), b)
	gomega.Expect(b.Type).To(gomega.BeEquivalentTo(typ))
	q, err := token2.ToQuantity(b.Quantity, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	expectedQ := token2.NewQuantityFromUInt64(expected)
	gomega.Expect(expectedQ.Cmp(q)).To(gomega.BeEquivalentTo(0), "[%s]!=[%s]", expected, q)
}

func queryHouse(network *integration.Infrastructure, client *token3.NodeReference, houseID string, address string, errorMsgs ...string) {
	resBoxed, err := network.Client(client.ReplicaName()).CallView("queryHouse", common.JSONMarshall(house.GetHouse{
		HouseID: houseID,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		h := &house.House{}
		gomega.Expect(json.Unmarshal(resBoxed.([]byte), h)).NotTo(gomega.HaveOccurred())
		gomega.Expect(h.LinearID).To(gomega.BeEquivalentTo(houseID))
		gomega.Expect(h.Address).To(gomega.BeEquivalentTo(address))
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
	}
}
