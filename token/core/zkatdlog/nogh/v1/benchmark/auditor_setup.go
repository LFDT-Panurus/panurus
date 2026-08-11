/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	tokcommon "github.com/LFDT-Panurus/panurus/token/core/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	zkcommon "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	v1driver "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	issue_pkg "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace/noop"
)

// AuditCheckSetup holds a ready-to-call AuditorService together with a
// pre-built TokenRequest and TokenRequestMetadata for a single issue action.
// The request is generated once at setup time from real ZK proof material so
// that AuditorCheck exercises the actual Pedersen commitment arithmetic on
// every benchmark iteration.
type AuditCheckSetup struct {
	Service  *v1.AuditorService
	Request  *driver.TokenRequest
	Metadata *driver.TokenRequestMetadata
	Anchor   driver.TokenRequestAnchor
}

// NewAuditCheckSetup constructs an AuditCheckSetup from a SetupConfiguration.
// It generates a real ZK issue action using the crypto layer directly, then
// wires an AuditorService with the driver Deserializer so that identity
// verification matches production behaviour.
func NewAuditCheckSetup(conf *SetupConfiguration) (*AuditCheckSetup, error) {
	// Build a real issue action using the crypto layer so that the Pedersen
	// commitment in the action is genuine and AuditorCheck performs real arithmetic.
	issuerID := conf.IssuerSigner.ID
	signer := &zkcommon.WrappedSigningIdentity{
		Identity: issuerID,
		Signer:   conf.IssuerSigner.Signer,
	}

	values := []uint64{100}
	owners := [][]byte{conf.OwnerIdentity.ID}

	issuer := issue_pkg.NewIssuer("ABC", signer, conf.PP)
	issueAction, zkMeta, err := issuer.GenerateZKIssue(values, owners)
	if err != nil {
		return nil, errors.Wrap(err, "failed generating ZK issue for audit check setup")
	}

	issueActionRaw, err := issueAction.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing issue action for audit check setup")
	}

	metaBytes, err := zkMeta[0].Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing token metadata for audit check setup")
	}

	request := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: issueActionRaw},
		},
	}

	// The issuer identity stored in the action (ia.Issuer) equals issuerID.
	// Setting IssueMetadata.Issuer.Identity to the same value satisfies the
	// identity-equality check inside audit.GetAuditInfoForIssues.
	metadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{
				ActionID: 0,
				IssueMetadata: &driver.IssueMetadata{
					Issuer: driver.AuditableIdentity{
						Identity:  issuerID,
						AuditInfo: conf.IssuerSigner.AuditInfo,
					},
					Outputs: []*driver.IssueOutputMetadata{
						{
							OutputMetadata: metaBytes,
							Receivers: []*driver.AuditableIdentity{
								{
									Identity:  conf.OwnerIdentity.ID,
									AuditInfo: conf.OwnerIdentity.AuditInfo,
								},
							},
						},
					},
				},
			},
		},
	}

	ppm, err := tokcommon.NewPublicParamsManagerFromParams(conf.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating public params manager for audit check setup")
	}

	deserializer, err := v1driver.NewDeserializer(conf.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating deserializer for audit check setup")
	}

	service := v1.NewAuditorService(
		logging.MustGetLogger(),
		ppm,
		deserializer,
		&mock.QueryEngine{},
		noop.NewTracerProvider(),
	)

	return &AuditCheckSetup{
		Service:  service,
		Request:  request,
		Metadata: metadata,
		Anchor:   "benchmark-anchor",
	}, nil
}
