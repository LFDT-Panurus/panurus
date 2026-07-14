/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"time"

	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/LFDT-Panurus/panurus/integration/token/common/views"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/onsi/gomega"
)

func CheckFinality(network *integration.Infrastructure, id *token2.NodeReference, txID string, tmsID *token.TMSID, fail bool) {
	if id == nil || len(id.Id()) == 0 {
		return
	}
	_, err := network.Client(id.ReplicaName()).CallView("TxFinality", common.JSONMarshall(&views.TxFinality{
		TxID:    txID,
		TMSID:   tmsID,
		Timeout: 30 * time.Second,
	}))
	if fail {
		gomega.Expect(err).To(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
}

func CheckEndorserFinality(network *integration.Infrastructure, id *token2.NodeReference, txID string, tmsID *token.TMSID, fail bool) {
	if id == nil || len(id.Id()) == 0 {
		return
	}
	if tmsID == nil {
		tmsID = &token.TMSID{}
	}
	_, err := network.Client(id.ReplicaName()).CallView("EndorserFinality", common.JSONMarshall(&endorser.Finality{
		TxID:    txID,
		Network: tmsID.Network,
		Channel: tmsID.Channel,
	}))
	if fail {
		gomega.Expect(err).To(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
}
