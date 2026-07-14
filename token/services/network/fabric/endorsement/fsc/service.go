/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"math/rand"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var (
	logger = logging.MustGetLogger()
)

const (
	AmIAnEndorserKey = "services.network.fabric.fsc_endorsement.endorser"
	EndorsersKey     = "services.network.fabric.fsc_endorsement.endorsers"
	PolicyType       = "services.network.fabric.fsc_endorsement.policy.type"

	OneOutNPolicy = "1outn"
	AllPolicy     = "all"
)

type EndorsementService struct {
	TmsID           token.TMSID
	Endorsers       []view.Identity
	ViewManager     ViewManager
	PolicyType      string
	EndorserService EndorserService
}

func NewEndorsementService(
	namespaceProcessor NamespaceTxProcessor,
	tmsID token.TMSID,
	configuration tdriver.Configuration,
	viewRegistry ViewRegistry,
	viewManager ViewManager,
	identityProvider IdentityProvider,
	keyTranslator translator.KeyTranslator,
	getTranslator TranslatorProviderFunc,
	endorserService EndorserService,
	tokenManagementSystemProvider TokenManagementSystemProvider,
	storageProvider StorageProvider,
	channelProvider ChannelProvider,
) (*EndorsementService, error) {
	if configuration.GetBool(AmIAnEndorserKey) {
		logger.Debug("this node is an endorser, prepare it...")
		if err := namespaceProcessor.EnableTxProcessing(tmsID); err != nil {
			return nil, errors.WithMessagef(err, "failed to add namespace to committer [%s]", tmsID)
		}
		if err := viewRegistry.RegisterResponder(
			NewRequestApprovalResponderView(
				keyTranslator,
				getTranslator,
				endorserService,
				tokenManagementSystemProvider,
				storageProvider,
				channelProvider,
			),
			&RequestApprovalView{},
		); err != nil {
			return nil, errors.WithMessagef(err, "failed to register approval view for [%s]", tmsID)
		}
	} else {
		logger.Debugf("this node is an not endorser, is key set? [%v].", configuration.IsSet(AmIAnEndorserKey))
	}

	policyType := configuration.GetString(PolicyType)
	if len(policyType) == 0 {
		policyType = AllPolicy
	}

	var endorserIDs []string
	if err := configuration.UnmarshalKey(EndorsersKey, &endorserIDs); err != nil {
		return nil, errors.WithMessagef(err, "failed to load endorsers")
	}
	logger.Debugf("defined [%s] as endorsers for [%s]", endorserIDs, tmsID)
	if len(endorserIDs) == 0 {
		return nil, errors.Errorf("no endorsers found for [%s]", tmsID)
	}
	endorsers := make([]view.Identity, 0, len(endorserIDs))
	for _, id := range endorserIDs {
		if endorserID := identityProvider.Identity(id); endorserID.IsNone() {
			return nil, errors.Errorf("cannot find identity for endorser [%s]", id)
		} else {
			endorsers = append(endorsers, endorserID)
		}
	}

	return &EndorsementService{
		Endorsers:       endorsers,
		TmsID:           tmsID,
		ViewManager:     viewManager,
		PolicyType:      policyType,
		EndorserService: endorserService,
	}, nil
}

func (e *EndorsementService) Endorse(context view.Context, requestRaw []byte, signer view.Identity, txID driver.TxID, metadata driver.TransientMap) (driver.Envelope, error) {
	var endorsers []view.Identity
	switch e.PolicyType {
	case OneOutNPolicy:
		endorsers = []view.Identity{e.Endorsers[rand.Intn(len(e.Endorsers))]}
	case AllPolicy:
		endorsers = e.Endorsers
	default:
		endorsers = e.Endorsers
	}
	logger.DebugfContext(context.Context(), "request approval via panurus endrosers with policy [%s]: [%d]...", e.PolicyType, len(endorsers))

	envBoxed, err := e.ViewManager.InitiateView(context.Context(), NewRequestApprovalView(
		e.TmsID,
		txID,
		requestRaw,
		nil,
		endorsers,
		e.EndorserService,
		metadata,
	))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to request approval")
	}
	env, ok := envBoxed.(driver.Envelope)
	if !ok {
		return nil, errors.Errorf("expected driver.Envelope, got [%T]", envBoxed)
	}

	return env, nil
}
