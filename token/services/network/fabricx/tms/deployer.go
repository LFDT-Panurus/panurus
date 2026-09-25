/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"crypto/sha256"
	"reflect"
	"strings"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
)

var logger = logging.MustGetLogger()

// ppFetcher models an interface for fetching public parameters and namespace version.
type ppFetcher interface {
	Fetch(network cdriver.Network, channel cdriver.Channel, namespace cdriver.Namespace) ([]byte, error)
	FetchNamespaceVersion(network cdriver.Network, channel cdriver.Channel, namespace cdriver.Namespace) (uint64, error)
}

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
	ppFetcher     ppFetcher
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
// submits it to the network. It fetches the current namespace policy version
// so the committer can validate the transaction against the correct policy epoch.
// If submit fails because the transaction was rejected as invalid (e.g. due to
// a policy epoch or namespace version conflict from a concurrent namespace policy update
// or a stale read from a lagging query service node), it re-reads the namespace version
// from the ledger and retries once.
// Retries are strictly gated on definitive transaction invalidation to avoid duplicate
// submissions on indeterminate submit outcomes (e.g. broadcast or finality timeouts).
func (s *deployerService) deployPublicParametersRaw(tmsID token.TMSID, ppRaw []byte) error {
	const maxAttempts = 2
	var lastErr error

	for attempt := range maxAttempts {
		nsVersion, err := s.ppFetcher.FetchNamespaceVersion(tmsID.Network, tmsID.Channel, tmsID.Namespace)
		if err != nil {
			logger.Errorf("Failed fetching namespace version for [%s] (attempt %d/%d): %v", tmsID, attempt+1, maxAttempts, err)

			return err
		}

		tx, err := s.createPublicParametersTx(ppRaw, tmsID.Namespace, nsVersion)
		if err != nil {
			return err
		}

		err = s.nsSubmitter.Submit(tmsID.Network, tmsID.Channel, tx)
		if err == nil {
			return nil
		}
		lastErr = err

		if attempt < maxAttempts-1 && isTransactionInvalidError(err) {
			logger.Warnf("PP deployment transaction rejected for [%s] (attempt %d/%d), re-reading namespace version and retrying: %v",
				tmsID, attempt+1, maxAttempts, err)

			continue
		}

		return err
	}

	return lastErr
}

// isTransactionInvalidError reports whether err represents a definitive transaction rejection
// by the committer (e.g. finality error containing "transaction [...] is invalid"),
// as opposed to an indeterminate submit outcome such as broadcast failure or timeout ("max retries reached").
func isTransactionInvalidError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()

	return strings.Contains(msg, "is invalid") ||
		strings.Contains(msg, "ABORTED_MVCC_CONFLICT") ||
		strings.Contains(msg, "MVCC_READ_CONFLICT") ||
		strings.Contains(msg, "version mismatch")
}

// createPublicParametersTx builds a FabricX transaction that writes the raw
// public parameters and their SHA256 hash to the ledger using the setup keys.
func (s *deployerService) createPublicParametersTx(ppRaw []byte, namespaceID cdriver.Namespace, nsVersion uint64) (*applicationpb.Tx, error) {
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
			NsVersion:   nsVersion,
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
