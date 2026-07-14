/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	tokcommon "github.com/LFDT-Panurus/panurus/token/core/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	v1driver "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// IssueSetup holds a ready-to-call IssueService together with the invocation
// parameters for a single benchmark iteration. All fields are pre-computed at
// setup time so that the benchmark loop itself contains only the hot path.
type IssueSetup struct {
	Service   *v1.IssueService
	IssuerID  driver.Identity
	TokenType token2.Type
	Values    []uint64
	Owners    [][]byte
}

// NewIssueSetup constructs an IssueSetup from a SetupConfiguration.
// It stubs WalletService with a counterfeiter mock, wiring the real IssuerSigner
// from the configuration so that GenerateZKIssue exercises the actual ECDSA
// signing path. The Deserializer is instantiated via the driver so that
// audit-info encoding matches production behaviour.
func NewIssueSetup(conf *SetupConfiguration) (*IssueSetup, error) {
	ppm, err := tokcommon.NewPublicParamsManagerFromParams(conf.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating public params manager for issue setup")
	}

	issuerWallet := &drivermock.IssuerWallet{}
	issuerWallet.GetSignerReturns(conf.IssuerSigner.Signer, nil)

	walletService := &drivermock.WalletService{}
	walletService.IssuerWalletReturns(issuerWallet, nil)

	deserializer, err := v1driver.NewDeserializer(conf.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating deserializer for issue setup")
	}

	issuerID, err := conf.IssuerSigner.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing issuer identity for issue setup")
	}

	service := v1.NewIssueService(
		logging.MustGetLogger(),
		ppm,
		walletService,
		deserializer,
		nil, // TokensService is not needed for benchmarks
		nil, // TokensUpgradeService is only used when issuerIdentity.IsNone()
	)

	return &IssueSetup{
		Service:   service,
		IssuerID:  issuerID,
		TokenType: "ABC",
		Values:    []uint64{100},
		Owners:    [][]byte{conf.OwnerIdentity.ID},
	}, nil
}
