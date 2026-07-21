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
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
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

	OneOutNPolicy   = "1outn"
	AllPolicy       = "all"
	NamespacePolicy = "namespace"
)

type EndorsementService struct {
	TmsID                         token.TMSID
	Endorsers                     []view.Identity
	ViewManager                   ViewManager
	PolicyType                    string
	EndorserService               EndorserService
	EndorserSelector              EndorserSelector
	TokenManagementSystemProvider TokenManagementSystemProvider
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
	endorserSelector EndorserSelector,
	ppValidator PublicParamsValidator,
	replayGuard replay.Guard,
) (*EndorsementService, error) {
	if configuration.GetBool(AmIAnEndorserKey) {
		logger.Debug("this node is an endorser, prepare it...")
		if err := namespaceProcessor.EnableTxProcessing(tmsID); err != nil {
			return nil, errors.WithMessagef(err, "failed to add namespace to committer [%s]", tmsID)
		}
		responderView := NewResponderView(
			keyTranslator,
			getTranslator,
			endorserService,
			tokenManagementSystemProvider,
			storageProvider,
			channelProvider,
			ppValidator,
			replayGuard,
		)
		if err := viewRegistry.RegisterResponder(responderView, &SetupPublicParamsView{}); err != nil {
			return nil, errors.WithMessagef(err, "failed to register public params setup view for [%s]", tmsID)
		}
		if err := viewRegistry.RegisterResponder(responderView, &RequestApprovalView{}); err != nil {
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
		endorserID, err := identityProvider.Identity(id)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get identity for endorser [%s]", id)
		} else {
			endorsers = append(endorsers, endorserID)
		}
	}

	return &EndorsementService{
		Endorsers:                     endorsers,
		TmsID:                         tmsID,
		ViewManager:                   viewManager,
		PolicyType:                    policyType,
		EndorserService:               endorserService,
		EndorserSelector:              endorserSelector,
		TokenManagementSystemProvider: tokenManagementSystemProvider,
	}, nil
}

// selectEndorsers returns the set of endorsers to contact according to the configured policy type.
func (e *EndorsementService) selectEndorsers(context view.Context) ([]view.Identity, error) {
	switch e.PolicyType {
	case OneOutNPolicy:
		return []view.Identity{e.Endorsers[rand.Intn(len(e.Endorsers))]}, nil
	case AllPolicy:
		return e.Endorsers, nil
	case NamespacePolicy:
		selected, err := e.EndorserSelector.SelectEndorsers(context.Context(), e.TmsID, e.Endorsers)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed selecting endorsers by namespace policy")
		}

		return selected, nil
	default:
		return e.Endorsers, nil
	}
}

func (e *EndorsementService) Endorse(context view.Context, requestRaw []byte, signer view.Identity, txID driver.TxID, metadata driver.TransientMap) (driver.Envelope, error) {
	endorsers, err := e.selectEndorsers(context)
	if err != nil {
		return nil, err
	}
	logger.DebugfContext(context.Context(), "request approval via panurus endorsers with policy [%s]: [%d]...", e.PolicyType, len(endorsers))

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

// SetupPublicParams submits new/updated public parameters for endorsement, following the same
// endorser-selection policy used by Endorse. On re-setup of a namespace that already has a TMS,
// the submission is signed with a locally-held current-issuer wallet, so the responder can
// authorize the change; first-time setup carries no signature.
func (e *EndorsementService) SetupPublicParams(context view.Context, publicParamsRaw []byte, signer view.Identity, txID driver.TxID) (driver.Envelope, error) {
	endorsers, err := e.selectEndorsers(context)
	if err != nil {
		return nil, err
	}
	logger.DebugfContext(context.Context(), "request public params setup via panurus endorsers with policy [%s]: [%d]...", e.PolicyType, len(endorsers))

	var ppSig *PublicParamsSignature
	tms, err := e.TokenManagementSystemProvider.GetManagementService(token.WithTMSID(e.TmsID))
	switch {
	case err == nil:
		ppSig, err = signPublicParamsWithCurrentIssuer(context, tms, publicParamsRaw)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to sign public parameters for re-setup of [%s]", e.TmsID)
		}
	case errors.Is(err, token.ErrTMSNotFound):
		// first-time setup: no existing TMS to be authorized by, no signature needed
	default:
		return nil, errors.WithMessagef(err, "failed to look up tms [%s] for public params setup", e.TmsID)
	}

	envBoxed, err := e.ViewManager.InitiateView(context.Context(), NewSetupPublicParamsView(
		e.TmsID,
		txID,
		publicParamsRaw,
		ppSig,
		endorsers,
		e.EndorserService,
	))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to request public params setup")
	}
	env, ok := envBoxed.(driver.Envelope)
	if !ok {
		return nil, errors.Errorf("expected driver.Envelope, got [%T]", envBoxed)
	}

	return env, nil
}

// signPublicParamsWithCurrentIssuer produces a detached signature over ppRaw using a locally
// held signer for one of the current public parameters' issuers, authorizing a re-setup.
func signPublicParamsWithCurrentIssuer(context view.Context, tms *token.ManagementService, ppRaw []byte) (*PublicParamsSignature, error) {
	issuers := tms.PublicParameters().Issuers()
	if len(issuers) == 0 {
		return nil, errors.Errorf("no issuers defined in current public parameters for [%s]", tms.ID())
	}

	var lastErr error
	for _, id := range issuers {
		wallet, err := tms.WalletManager().IssuerWallet(context.Context(), id)
		if err != nil {
			lastErr = err

			continue
		}
		signer, err := wallet.GetSigner(context.Context(), id)
		if err != nil {
			lastErr = err

			continue
		}
		sigma, err := signer.Sign(ppRaw)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to sign public parameters with issuer [%s]", id)
		}

		return &PublicParamsSignature{SignerIdentity: id, Signature: sigma}, nil
	}

	return nil, errors.WithMessagef(lastErr, "no local signer found for any current issuer of [%s]", tms.ID())
}
