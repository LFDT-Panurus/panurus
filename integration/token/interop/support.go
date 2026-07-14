/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interop

import (
	"crypto"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token3 "github.com/LFDT-Panurus/panurus/integration/token"
	common2 "github.com/LFDT-Panurus/panurus/integration/token/common"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	views2 "github.com/LFDT-Panurus/panurus/integration/token/interop/views"
	"github.com/LFDT-Panurus/panurus/integration/token/interop/views/htlc"
	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/onsi/gomega"
)

func RegisterAuditor(network *integration.Infrastructure, opts ...token.ServiceOption) {
	options, err := token.CompileServiceOptions(opts...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	_, err = network.Client("auditor").CallView("registerAuditor", common.JSONMarshall(&views2.RegisterAuditor{
		TMSID: options.TMSID(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func IssueCash(network *integration.Infrastructure, wallet string, typ token2.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference) string {
	txid, err := network.Client("issuer").CallView("issue", common.JSONMarshall(&views.IssueCash{
		IssuerWallet: wallet,
		TokenType:    typ,
		Quantity:     amount,
		Recipient:    network.Identity(receiver.Id()),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txid)
	common2.CheckFinality(network, receiver, txID, nil, false)
	common2.CheckFinality(network, auditor, txID, nil, false)

	return common.JSONUnmarshalString(txid)
}

func IssueCashWithTMS(network *integration.Infrastructure, tmsID token.TMSID, issuer *token3.NodeReference, wallet string, typ token2.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference) string {
	txid, err := network.Client(issuer.ReplicaName()).CallView("issue", common.JSONMarshall(&views2.IssueCash{
		TMSID:        tmsID,
		IssuerWallet: wallet,
		TokenType:    typ,
		Quantity:     amount,
		Recipient:    network.Identity(receiver.Id()),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	txID := common.JSONUnmarshalString(txid)
	common2.CheckFinality(network, receiver, txID, &tmsID, false)
	common2.CheckFinality(network, auditor, txID, &tmsID, false)

	return txID
}

func ListIssuerHistory(network *integration.Infrastructure, wallet string, typ token2.Type) *token2.IssuedTokens {
	res, err := network.Client("issuer").CallView("history", common.JSONMarshall(&views.ListIssuedTokens{
		Wallet:    wallet,
		TokenType: typ,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	issuedTokens := &token2.IssuedTokens{}
	common.JSONUnmarshal(res.([]byte), issuedTokens)

	return issuedTokens
}

func CheckBalance(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expected uint64, opts ...token.ServiceOption) {
	options, err := token.CompileServiceOptions(opts...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	res, err := network.Client(id.ReplicaName()).CallView("balance", common.JSONMarshall(&views2.Balance{
		Wallet: wallet,
		Type:   typ,
		TMSID: token.TMSID{
			Network:   options.Network,
			Channel:   options.Channel,
			Namespace: options.Namespace,
		},
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	b := &views2.BalanceResult{}
	common.JSONUnmarshal(res.([]byte), b)
	gomega.Expect(b.Type).To(gomega.BeEquivalentTo(typ))
	q, err := token2.ToQuantity(b.Quantity, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	expectedQ := token2.NewQuantityFromUInt64(expected)
	gomega.Expect(expectedQ.Cmp(q)).To(gomega.BeEquivalentTo(0), "[%s]!=[%s]", expected, q)
}

func CheckBalanceReturnError(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expected uint64, opts ...token.ServiceOption) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("check balance panicked with err [%v]", r)
		}
	}()
	CheckBalance(network, id, wallet, typ, expected, opts...)

	return nil
}

func CheckHolding(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expected uint64, opts ...token.ServiceOption) {
	opt, err := token.CompileServiceOptions(opts...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to compile options [%v]", opts)
	tmsId := opt.TMSID()
	eIDBoxed, err := network.Client(id.ReplicaName()).CallView("GetEnrollmentID", common.JSONMarshall(&views.GetEnrollmentID{
		Wallet: wallet,
		TMSID:  &tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	eID := common.JSONUnmarshalString(eIDBoxed)
	holdingBoxed, err := network.Client("auditor").CallView("holding", common.JSONMarshall(&views.CurrentHolding{
		EnrollmentID: eID,
		TokenType:    typ,
		TMSID:        tmsId,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	holding, err := strconv.ParseUint(common.JSONUnmarshalString(holdingBoxed), 10, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(holding).To(gomega.Equal(expected))
}

func CheckBalanceWithLocked(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expected uint64, expectedLocked uint64, expectedExpired uint64, opts ...token.ServiceOption) {
	opt, err := token.CompileServiceOptions(opts...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to compile options [%v]", opts)
	resBoxed, err := network.Client(id.ReplicaName()).CallView("balance", common.JSONMarshall(&views2.Balance{
		Wallet: wallet,
		Type:   typ,
		TMSID:  opt.TMSID(),
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	result := &views2.BalanceResult{}
	common.JSONUnmarshal(resBoxed.([]byte), result)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	balance, err := strconv.ParseUint(result.Quantity, 10, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), 10, 64)
	locked, err := strconv.ParseUint(result.Locked, 10, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	expired, err := strconv.ParseUint(result.Expired, 10, 64)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	gomega.Expect(balance).To(gomega.Equal(expected), "expected [%d], got [%d]", expected, balance)                       // #nosec G115
	gomega.Expect(locked).To(gomega.Equal(expectedLocked), "expected locked [%d], got [%d]", expectedLocked, locked)      // #nosec G115
	gomega.Expect(expired).To(gomega.Equal(expectedExpired), "expected expired [%d], got [%d]", expectedExpired, expired) // #nosec G115
}

func CheckBalanceAndHolding(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expected uint64, opts ...token.ServiceOption) {
	CheckBalance(network, id, wallet, typ, expected, opts...)
	CheckHolding(network, id, wallet, typ, expected, opts...)
}

// #nosec G115
func CheckBalanceWithLockedAndHolding(network *integration.Infrastructure, id *token3.NodeReference, wallet string, typ token2.Type, expectedBalance uint64, expectedLocked uint64, expectedExpired uint64, expectedHolding int64, opts ...token.ServiceOption) {
	CheckBalanceWithLocked(network, id, wallet, typ, expectedBalance, expectedLocked, expectedExpired, opts...)
	if expectedHolding == -1 {
		expectedHolding = int64(expectedBalance + expectedLocked + expectedExpired)
	}
	CheckHolding(network, id, wallet, typ, uint64(expectedHolding), opts...)
}

func CheckPublicParams(network *integration.Infrastructure, tmsID token.TMSID, ids ...*token3.NodeReference) {
	for _, id := range ids {
		for _, replicaName := range id.AllNames() {
			_, err := network.Client(replicaName).CallView("CheckPublicParamsMatch", common.JSONMarshall(&views.CheckPublicParamsMatch{
				TMSID: &tmsID,
			}))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
	}
}

func CheckOwnerStore(network *integration.Infrastructure, tmsID token.TMSID, expectedErrors []string, ids ...*token3.NodeReference) {
	for _, id := range ids {
		for _, replicaName := range id.AllNames() {
			errorMessagesBoxed, err := network.Client(replicaName).CallView("CheckTTXDB", common.JSONMarshall(&views.CheckTTXDB{
				TMSID: tmsID,
			}))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			var errorMessages []string
			common.JSONUnmarshal(errorMessagesBoxed.([]byte), &errorMessages)

			gomega.Expect(errorMessages).To(gomega.HaveLen(len(expectedErrors)), "expected %d error messages from [%s], got [% v]", len(expectedErrors), id, errorMessages)
			for _, expectedError := range expectedErrors {
				found := slices.Contains(errorMessages, expectedError)
				gomega.Expect(found).To(gomega.BeTrue(), "cannot find error message [%s] in [% v]", expectedError, errorMessages)
			}
		}
	}
}

func CheckAuditorStore(network *integration.Infrastructure, tmsID token.TMSID, auditor *token3.NodeReference, walletID string, errorCheck func([]string) error) {
	errorMessagesBoxed, err := network.Client(auditor.ReplicaName()).CallView("CheckTTXDB", common.JSONMarshall(&views.CheckTTXDB{
		Auditor:         true,
		AuditorWalletID: walletID,
		TMSID:           tmsID,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	if errorCheck != nil {
		var errorMessages []string
		common.JSONUnmarshal(errorMessagesBoxed.([]byte), &errorMessages)
		gomega.Expect(errorCheck(errorMessages)).NotTo(gomega.HaveOccurred(), "failed to check errors")
	} else {
		var errorMessages []string
		common.JSONUnmarshal(errorMessagesBoxed.([]byte), &errorMessages)
		gomega.Expect(errorMessages).To(gomega.BeEmpty(), "expected 0 error messages, got [% v]", errorMessages)
	}
}

func PruneInvalidUnspentTokens(network *integration.Infrastructure, tmsID token.TMSID, ids ...*token3.NodeReference) {
	for _, id := range ids {
		eIDBoxed, err := network.Client(id.ReplicaName()).CallView("PruneInvalidUnspentTokens", common.JSONMarshall(&views.PruneInvalidUnspentTokens{TMSID: tmsID}))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var deleted []*token2.ID
		common.JSONUnmarshal(eIDBoxed.([]byte), &deleted)
		gomega.Expect(deleted).To(gomega.BeEmpty())
	}
}

func ListVaultUnspentTokens(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference) []*token2.ID {
	res, err := network.Client(id.ReplicaName()).CallView("ListVaultUnspentTokens", common.JSONMarshall(&views.ListVaultUnspentTokens{TMSID: tmsID}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	unspentTokens := &token2.UnspentTokens{}
	common.JSONUnmarshal(res.([]byte), unspentTokens)
	count := unspentTokens.Count()
	var IDs []*token2.ID
	for i := range count {
		tok := unspentTokens.At(i)
		IDs = append(IDs, &tok.Id)
	}

	return IDs
}

func CheckIfExistsInVault(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference, tokenIDs []*token2.ID) {
	_, err := network.Client(id.ReplicaName()).CallView("CheckIfExistsInVault", common.JSONMarshall(&views.CheckIfExistsInVault{TMSID: tmsID, IDs: tokenIDs}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func Restart(network *integration.Infrastructure, ids ...*token3.NodeReference) {
	for _, id := range ids {
		network.StopFSCNode(id.Id())
	}
	time.Sleep(10 * time.Second)
	for _, id := range ids {
		network.StartFSCNode(id.Id())
	}
	time.Sleep(10 * time.Second)
}

func HTLCLock(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference, wallet string, typ token2.Type, amount uint64, receiver *token3.NodeReference, auditor *token3.NodeReference, deadline time.Duration, hash []byte, hashFunc crypto.Hash, errorMsgs ...string) (string, []byte, []byte) {
	result, err := network.Client(id.ReplicaName()).CallView("htlc.lock", common.JSONMarshall(&htlc.Lock{
		TMSID:               tmsID,
		ReclamationDeadline: deadline,
		Wallet:              wallet,
		Type:                typ,
		Amount:              amount,
		Recipient:           network.Identity(receiver.Id()),
		Hash:                hash,
		HashFunc:            hashFunc,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		lockResult := &htlc.LockInfo{}
		common.JSONUnmarshal(result.([]byte), lockResult)

		common2.CheckFinality(network, receiver, lockResult.TxID, &tmsID, false)
		common2.CheckFinality(network, auditor, lockResult.TxID, &tmsID, false)

		if len(hash) == 0 {
			gomega.Expect(lockResult.PreImage).NotTo(gomega.BeNil())
		}
		gomega.Expect(lockResult.Hash).NotTo(gomega.BeNil())
		if len(hash) != 0 {
			gomega.Expect(lockResult.Hash).To(gomega.BeEquivalentTo(hash))
		}

		return lockResult.TxID, lockResult.PreImage, lockResult.Hash
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
		time.Sleep(5 * time.Second)

		errMsg := err.Error()
		fmt.Printf("Got error message [%s]\n", errMsg)
		txID := ""
		index := strings.Index(err.Error(), "<<<[")
		if index != -1 {
			txID = errMsg[index+4 : index+strings.Index(err.Error()[index:], "]>>>")]
		}
		fmt.Printf("Got error message, extracted tx id [%s]\n", txID)

		return txID, nil, nil
	}
}

func HTLCReclaimAll(network *integration.Infrastructure, id *token3.NodeReference, wallet string, errorMsgs ...string) {
	txID, err := network.Client(id.ReplicaName()).CallView("htlc.reclaimAll", common.JSONMarshall(&htlc.ReclaimAll{
		Wallet: wallet,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, id, common.JSONUnmarshalString(txID), nil, false)
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
		time.Sleep(5 * time.Second)
	}
}

func HTLCReclaimByHash(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference, wallet string, hash []byte, errorMsgs ...string) {
	txID, err := network.Client(id.ReplicaName()).CallView("htlc.reclaimByHash", common.JSONMarshall(&htlc.ReclaimByHash{
		Wallet: wallet,
		Hash:   hash,
		TMSID:  tmsID,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		common2.CheckFinality(network, id, common.JSONUnmarshalString(txID), &tmsID, false)
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
		time.Sleep(5 * time.Second)
	}
}

func HTLCCheckExistenceReceivedExpiredByHash(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference, wallet string, hash []byte, exists bool, errorMsgs ...string) {
	_, err := network.Client(id.ReplicaName()).CallView("htlc.CheckExistenceReceivedExpiredByHash", common.JSONMarshall(&htlc.CheckExistenceReceivedExpiredByHash{
		Wallet: wallet,
		Hash:   hash,
		Exists: exists,
		TMSID:  tmsID,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
	}
}

func htlcClaim(network *integration.Infrastructure, tmsID token.TMSID, id *token3.NodeReference, wallet string, preImage []byte, auditor *token3.NodeReference, errorMsgs ...string) string {
	txIDBoxed, err := network.Client(id.ReplicaName()).CallView("htlc.claim", common.JSONMarshall(&htlc.Claim{
		TMSID:    tmsID,
		Wallet:   wallet,
		PreImage: preImage,
	}))
	if len(errorMsgs) == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		txID := common.JSONUnmarshalString(txIDBoxed)
		common2.CheckFinality(network, id, txID, &tmsID, false)
		common2.CheckFinality(network, auditor, txID, &tmsID, false)

		return txID
	} else {
		gomega.Expect(err).To(gomega.HaveOccurred())
		for _, msg := range errorMsgs {
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(msg))
		}
		time.Sleep(5 * time.Second)

		errMsg := err.Error()
		fmt.Printf("Got error message [%s]\n", errMsg)
		txID := ""
		index := strings.Index(err.Error(), "<<<[")
		if index != -1 {
			txID = errMsg[index+4 : index+strings.Index(err.Error()[index:], "]>>>")]
		}
		fmt.Printf("Got error message, extracted tx id [%s]\n", txID)

		return txID
	}
}

func fastExchange(network *integration.Infrastructure, id *token3.NodeReference, recipient *token3.NodeReference, tmsID1 token.TMSID, typ1 token2.Type, amount1 uint64, tmsID2 token.TMSID, typ2 token2.Type, amount2 uint64, deadline time.Duration) {
	_, err := network.Client(id.ReplicaName()).CallView("htlc.fastExchange", common.JSONMarshall(&htlc.FastExchange{
		Recipient:           network.Identity(recipient.Id()),
		TMSID1:              tmsID1,
		Type1:               typ1,
		Amount1:             amount1,
		TMSID2:              tmsID2,
		Type2:               typ2,
		Amount2:             amount2,
		ReclamationDeadline: deadline,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// give time to bob to commit the transaction
	time.Sleep(10 * time.Second)
}

func scan(network *integration.Infrastructure, id *token3.NodeReference, hash []byte, hashFunc crypto.Hash, opts ...token.ServiceOption) {
	options, err := token.CompileServiceOptions(opts...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	_, err = network.Client(id.ReplicaName()).CallView("htlc.scan", common.JSONMarshall(&htlc.Scan{
		TMSID:    options.TMSID(),
		Timeout:  3 * time.Minute,
		Hash:     hash,
		HashFunc: hashFunc,
	}))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}
