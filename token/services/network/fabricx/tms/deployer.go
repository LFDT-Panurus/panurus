/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"crypto/sha256"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
)

var logger = logging.MustGetLogger()

// DeployerService models a service for deploying TMSs.
type DeployerService interface {
	// DeployTMSs deploys all TMSs defined in the configuration.
	DeployTMSs() error
	// DeployTMS deploys the TMS with the given ID.
	DeployTMS(tmsID token.TMSID) error
	// DeployTMSWithPP deploys the TMS with the given ID and public parameters.
	DeployTMSWithPP(tmsID token.TMSID, ppRaw []byte) error
}

// NewTMSDeployerService returns a new DeployerService instance for
// token management systems.
func NewTMSDeployerService(
	ppFetcher *pp.PublicParametersService,
	configService *config.Service,
	nsSubmitter Submitter,
) *deployerService {
	return &deployerService{
		ppFetcher:     ppFetcher,
		configService: configService,
		nsSubmitter:   nsSubmitter,
		keyTranslator: &keys.Translator{},
	}
}

type deployerService struct {
	ppFetcher     fabric.NetworkPublicParamsFetcher
	configService *config.Service
	nsSubmitter   Submitter
	keyTranslator translator.KeyTranslator
}

// GetTMSIDs returns all token management system IDs defined in the configuration.
func (s *deployerService) GetTMSIDs() ([]token.TMSID, error) {
	tmsIDs := make([]token.TMSID, 0)

	// TMSs
	tmss, err := s.configService.Configurations()
	if err != nil {
		logger.Errorf("Failed getting TMS configurations: %v", err)

		return nil, err
	}
	for _, tms := range tmss {
		tmsIDs = append(tmsIDs, tms.ID())
	}
	logger.Infof("Found %d namespaces under TMSs", len(tmss))

	return tmsIDs, nil
}

// DeployTMSs iterates through all configured token management systems
// and triggers their deployment.
func (s *deployerService) DeployTMSs() error {
	logger.Infof("Deploying TMSs...")

	tmsIDs, err := s.GetTMSIDs()
	if err != nil {
		return err
	}

	logger.Infof("Found %d TMS IDs to deploy: [%v]", len(tmsIDs), tmsIDs)

	for _, tmsID := range tmsIDs {
		logger.Infof("Deploying TMS [%s]", tmsID)
		if err := s.DeployTMS(tmsID); err != nil {
			logger.Errorf("Failed deploying TMS [%s]: %v", tmsID, err)

			return err
		}
	}

	return nil
}

// DeployTMS fetches the public parameters for the specified TMS and deploys them.
func (s *deployerService) DeployTMS(tmsID token.TMSID) error {
	return s.deployPublicParameters(tmsID)
}

// DeployTMSWithPP deploys the provided raw public parameters for the specified TMS.
func (s *deployerService) DeployTMSWithPP(tmsID token.TMSID, ppRaw []byte) error {
	return s.deployPublicParametersRaw(tmsID, ppRaw)
}

// deployPublicParameters fetches public parameters and passes them to the deployment logic.
func (s *deployerService) deployPublicParameters(tmsID token.TMSID) error {
	ppRaw, err := s.ppFetcher.Fetch(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return err
	}

	return s.deployPublicParametersRaw(tmsID, ppRaw)
}

// deployPublicParametersRaw constructs a public parameters transaction and
// submits it to the network.
func (s *deployerService) deployPublicParametersRaw(tmsID token.TMSID, ppRaw []byte) error {
	tx, err := s.createPublicParametersTx(ppRaw, tmsID.Namespace)
	if err != nil {
		return err
	}

	return s.nsSubmitter.Submit(tmsID.Network, tmsID.Channel, tx)
}

// createPublicParametersTx builds a FabricX transaction that writes the raw
// public parameters and their SHA256 hash to the ledger using the setup keys.
func (s *deployerService) createPublicParametersTx(ppRaw []byte, namespaceID cdriver.Namespace) (*applicationpb.Tx, error) {
	key, err := s.keyTranslator.CreateSetupKey()
	if err != nil {
		return nil, err
	}
	keyHash, err := s.keyTranslator.CreateSetupHashKey()
	if err != nil {
		return nil, err
	}

	valueHash := sha256.Sum256(ppRaw)
	tx := &applicationpb.Tx{
		Namespaces: []*applicationpb.TxNamespace{{
			NsId:        namespaceID,
			NsVersion:   0,
			ReadsOnly:   []*applicationpb.Read{{Key: []byte("initialized")}},
			BlindWrites: []*applicationpb.Write{{Key: []byte(key), Value: ppRaw}, {Key: []byte(keyHash), Value: valueHash[:]}},
		}},
	}

	return tx, nil
}

// GetTMSDeployerService returns the DeployerService instance from the service provider.
func GetTMSDeployerService(sp services.Provider) (DeployerService, error) {
	s, err := sp.GetService(reflect.TypeFor[*DeployerService]())
	if err != nil {
		return nil, err
	}

	return s.(DeployerService), nil
}
