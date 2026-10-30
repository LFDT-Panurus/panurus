/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"crypto/sha256"
	"os"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
)

var logger = logging.MustGetLogger()

// DeployerService models a service for deploying TMSs.
type DeployerService interface {
	// DeployTMSs deploys all TMSs defined in the configuration.
	DeployTMSs() error
	// DeployTMS deploys the TMS with the given ID, using the public parameters at the
	// publicParameters.path of its configuration.
	DeployTMS(tmsID token.TMSID) error
	// DeployTMSWithPP deploys the TMS with the given ID and public parameters.
	DeployTMSWithPP(tmsID token.TMSID, ppRaw []byte) error
}

// NewTMSDeployerService returns a new DeployerService instance for
// token management systems.
func NewTMSDeployerService(
	configService *config.Service,
	nsSubmitter Submitter,
) *deployerService {
	return &deployerService{
		configService: configService,
		nsSubmitter:   nsSubmitter,
		keyTranslator: &keys.Translator{},
	}
}

type deployerService struct {
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

// DeployTMS loads the public parameters of the specified TMS from its configuration and deploys
// them.
func (s *deployerService) DeployTMS(tmsID token.TMSID) error {
	return s.deployPublicParameters(tmsID)
}

// DeployTMSWithPP deploys the provided raw public parameters for the specified TMS.
func (s *deployerService) DeployTMSWithPP(tmsID token.TMSID, ppRaw []byte) error {
	return s.deployPublicParametersRaw(tmsID, ppRaw)
}

// deployPublicParameters loads the public parameters from the TMS configuration and passes them to
// the deployment logic. They are not read from the ledger: on a namespace that was never initialized
// there is nothing there yet, and deploying is how they get there.
func (s *deployerService) deployPublicParameters(tmsID token.TMSID) error {
	ppRaw, err := s.publicParametersFromConfig(tmsID)
	if err != nil {
		return err
	}

	return s.deployPublicParametersRaw(tmsID, ppRaw)
}

// publicParametersFromConfig reads the file at the publicParameters.path of the TMS configuration.
func (s *deployerService) publicParametersFromConfig(tmsID token.TMSID) ([]byte, error) {
	tmsConfig, err := s.configService.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load the configuration of [%s]", tmsID)
	}
	var cPP struct {
		Path string `yaml:"path"`
	}
	if err := tmsConfig.UnmarshalKey("publicParameters", &cPP); err != nil {
		return nil, errors.WithMessagef(err, "failed to unmarshal the public parameters configuration of [%s]", tmsID)
	}
	if len(cPP.Path) == 0 {
		return nil, errors.Errorf("no public parameters path configured for [%s]", tmsID)
	}
	logger.Infof("load public parameters for [%s] from [%s]", tmsID, cPP.Path)
	ppRaw, err := os.ReadFile(cPP.Path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load public parameters for [%s] from [%s]", tmsID, cPP.Path)
	}

	return ppRaw, nil
}

// deployPublicParametersRaw constructs a public parameters transaction and
// submits it to the network. Empty public parameters are refused: the
// transaction would commit and leave the namespace unusable.
func (s *deployerService) deployPublicParametersRaw(tmsID token.TMSID, ppRaw []byte) error {
	if len(ppRaw) == 0 {
		return errors.Errorf("refusing to deploy empty public parameters for [%s]", tmsID)
	}
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
