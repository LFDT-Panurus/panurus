/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nft

import (
	"encoding/json"
	"time"

	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/nft/views"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/onsi/gomega"
)

func TestAll(network *integration.Infrastructure, sel *token3.ReplicaSelector) {
	issuer := sel.Get("issuer")
	alice := sel.Get("alice")
	bob := sel.Get("bob")
	auditor := sel.Get("auditor")

	// give some time to the nodes to get the public parameters
	time.Sleep(10 * time.Second)

	_, err := network.Client(auditor.ReplicaName()).CallView("registerAuditor", nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	houseID := issueHouse(network, issuer, alice, 4)
	queryHouse(network, alice, houseID, "5th Avenue")
	queryHouse(network, bob, houseID, "5th Avenue", "failed loading house with id")
	transferHouse(network, houseID, alice, bob)
	queryHouse(network, bob, houseID, "5th Avenue")
	queryHouse(network, alice, houseID, "5th Avenue", "failed loading house with id")
}

func issueHouse(network *integration.Infrastructure, issuer, recipient *token3.NodeReference, valuation uint64) string {
	houseIDBoxed, err := network.Client(issuer.ReplicaName()).CallView("issue", common.JSONMarshall(views.IssueHouse{
		IssuerWallet: "",
		Recipient:    recipient.Id(),
		Address:      "5th Avenue",
		Valuation:    valuation,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	time.Sleep(5 * time.Second)

	return common.JSONUnmarshalString(houseIDBoxed)
}

func transferHouse(network *integration.Infrastructure, houseID string, from, to *token3.NodeReference) {
	txIDBoxed, err := network.Client(from.ReplicaName()).CallView("transfer", common.JSONMarshall(views.Transfer{
		HouseID:   houseID,
		Recipient: to.Id(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	common2.CheckFinality(network, to, common.JSONUnmarshalString(txIDBoxed), nil, false)
}

func queryHouse(network *integration.Infrastructure, client *token3.NodeReference, houseID string, address string, errorMsgs ...string) {
	resBoxed, err := network.Client(client.ReplicaName()).CallView("queryHouse", common.JSONMarshall(views.GetHouse{
		HouseID: houseID,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		h := &views.House{}
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
