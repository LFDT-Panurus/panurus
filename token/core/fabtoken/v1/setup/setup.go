/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	encoding "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	fabpp "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

const (
	// FabTokenDriverName is name of the fabtoken driver
	FabTokenDriverName = driver.TokenDriverName("fabtoken")
	// ProtocolV1 is the v1 version
	ProtocolV1       = driver.TokenDriverVersion(1)
	DefaultPrecision = uint64(64)
)

// PublicParams is the public parameters for fabtoken
type PublicParams struct {
	// DriverName is the name of the token driver this public params refer to.
	DriverName driver.TokenDriverName
	// DriverVersion is the version of the token driver this public params refer to.
	DriverVersion driver.TokenDriverVersion
	// The precision of token quantities
	QuantityPrecision uint64
	// MaxToken is the maximum quantity a token can hold
	MaxToken uint64
	// This is set when audit is enabled
	Auditor []byte
	// This encodes the list of authorized issuers
	IssuerIDs []driver.Identity
	// ExtraData contains any extra custom data
	ExtraData driver.Extras
}

// Setup initializes PublicParams
func Setup(precision uint64) (*PublicParams, error) {
	return NewWith(FabTokenDriverName, ProtocolV1, precision)
}

// WithVersion is like Setup with the additional possibility to specify the version number
func WithVersion(precision uint64, version driver.TokenDriverVersion) (*PublicParams, error) {
	return NewWith(FabTokenDriverName, version, precision)
}

// NewPublicParamsFromBytes deserializes the raw bytes into public parameters
// The resulting public parameters are labeled with the passed label
func NewPublicParamsFromBytes(raw []byte, driverName driver.TokenDriverName, driverVersion driver.TokenDriverVersion) (*PublicParams, error) {
	params := &PublicParams{}
	params.DriverName = driverName
	params.DriverVersion = driverVersion
	if err := params.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal public parameters")
	}

	return params, nil
}

// NewWith returns a new instance of the public parameters using the given arguments
func NewWith(driverName driver.TokenDriverName, driverVersion driver.TokenDriverVersion, precision uint64) (*PublicParams, error) {
	if precision > 64 {
		return nil, errors.Errorf("invalid precision [%d], must be smaller or equal than 64", precision)
	}
	if precision == 0 {
		return nil, errors.New("invalid precision, should be greater than 0")
	}

	return &PublicParams{
		DriverName:        driverName,
		DriverVersion:     driverVersion,
		QuantityPrecision: precision,
		MaxToken:          uint64(1<<precision) - 1,
		ExtraData:         driver.Extras{},
	}, nil
}

// TokenDriverName return the token driver name this public params refer to
func (p *PublicParams) TokenDriverName() driver.TokenDriverName {
	return p.DriverName
}

func (p *PublicParams) TokenDriverVersion() driver.TokenDriverVersion {
	return p.DriverVersion
}

// TokenDataHiding indicates if the PublicParams corresponds to a driver that hides token data
// fabtoken does not hide token data, hence, TokenDataHiding returns false
func (p *PublicParams) TokenDataHiding() bool {
	return false
}

// CertificationDriver returns the label of the PublicParams
// From the label, one can deduce what certification process will be used if any.
func (p *PublicParams) CertificationDriver() string {
	return string(p.DriverName)
}

// GraphHiding indicates if the PublicParams corresponds to a driver that hides the transaction graph
// fabtoken does not hide the graph, hence, GraphHiding returns false
func (p *PublicParams) GraphHiding() bool {
	return false
}

// MaxTokenValue returns the maximum value that a token can hold according to PublicParams
func (p *PublicParams) MaxTokenValue() uint64 {
	return p.MaxToken
}

// Bytes marshals PublicParams
func (p *PublicParams) Bytes() ([]byte, error) {
	issuers, err := protos.ToProtosSliceFunc(p.IssuerIDs, func(id driver.Identity) (*driverv1.Identity, error) {
		return &driverv1.Identity{
			Raw: id,
		}, nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize issuer")
	}

	params := &fabpp.PublicParameters{
		TokenDriverName:    string(p.DriverName),
		TokenDriverVersion: uint32(p.DriverVersion),
		Auditor: &driverv1.Identity{
			Raw: p.Auditor,
		},
		Issuers:           issuers,
		MaxToken:          p.MaxToken,
		QuantityPrecision: p.QuantityPrecision,
		Metadata:          p.ExtraData,
	}

	return proto.Marshal(params)
}

func (p *PublicParams) FromBytes(data []byte) error {
	publicParams := &fabpp.PublicParameters{}
	if err := proto.Unmarshal(data, publicParams); err != nil {
		return errors.Wrapf(err, "failed to unmarshal public parameters")
	}
	p.DriverVersion = driver.TokenDriverVersion(publicParams.TokenDriverVersion)
	p.QuantityPrecision = publicParams.QuantityPrecision
	p.MaxToken = publicParams.MaxToken
	issuers, err := protos.FromProtosSliceFunc2(publicParams.Issuers, func(id *driverv1.Identity) (driver.Identity, error) {
		if id == nil {
			return nil, nil
		}

		return id.Raw, nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize issuers")
	}
	p.IssuerIDs = issuers
	if publicParams.Auditor != nil {
		p.Auditor = publicParams.Auditor.Raw
	}
	p.ExtraData = publicParams.Metadata
	if p.ExtraData == nil {
		p.ExtraData = driver.Extras{}
	}

	return nil
}

// Serialize marshals a wrapper around PublicParams (SerializedPublicParams)
func (p *PublicParams) Serialize() ([]byte, error) {
	raw, err := p.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize public parameters")
	}

	return encoding.Marshal(&pp.PublicParameters{
		Identifier: string(core.DriverIdentifier(p.DriverName, p.DriverVersion)),
		Raw:        raw,
	})
}

// Deserialize un-marshals the passed bytes into PublicParams
func (p *PublicParams) Deserialize(raw []byte) error {
	container, err := encoding.Unmarshal(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize public parameters")
	}
	expectedID := string(core.DriverIdentifier(p.DriverName, p.DriverVersion))
	if container.Identifier != expectedID {
		return errors.Errorf(
			"invalid identifier, expecting [%s], got [%s]",
			expectedID,
			container.Identifier,
		)
	}

	return p.FromBytes(container.Raw)
}

// AuditorIdentity returns the auditor identity encoded in PublicParams
func (p *PublicParams) AuditorIdentity() driver.Identity {
	return p.Auditor
}

// AddAuditor sets the Auditor field in PublicParams to the passed identity
func (p *PublicParams) AddAuditor(auditor driver.Identity) {
	p.Auditor = auditor
}

// AddIssuer adds the passed issuer to the array of Issuers in PublicParams
func (p *PublicParams) AddIssuer(issuer driver.Identity) {
	p.IssuerIDs = append(p.IssuerIDs, issuer)
}

// SetIssuers sets the issuers to the passed identities
func (p *PublicParams) SetIssuers(ids []driver.Identity) {
	p.IssuerIDs = ids
}

// SetAuditors sets the auditors to the passed identities
func (p *PublicParams) SetAuditors(ids []driver.Identity) {
	p.Auditor = ids[0]
}

// Auditors returns the list of authorized auditors
// fabtoken only supports a single auditor
func (p *PublicParams) Auditors() []driver.Identity {
	if len(p.Auditor) == 0 {
		return []driver.Identity{}
	}

	return []driver.Identity{p.Auditor}
}

// Issuers returns the list of authorized issuers
func (p *PublicParams) Issuers() []driver.Identity {
	return p.IssuerIDs
}

// Precision returns the quantity precision encoded in PublicParams
func (p *PublicParams) Precision() uint64 {
	return p.QuantityPrecision
}

// Validate validates the public parameters.
// The list of issues can be empty meaning that anyone can create tokens.
func (p *PublicParams) Validate() error {
	if p.QuantityPrecision > 64 {
		return errors.Errorf("invalid precision [%d], must be less than 64", p.QuantityPrecision)
	}
	if p.QuantityPrecision == 0 {
		return errors.New("invalid precision, must be greater than 0")
	}
	maxTokenValue := uint64(1<<p.Precision()) - 1
	if p.MaxToken > maxTokenValue {
		return errors.Errorf("max token value is invalid [%d]>[%d]", p.MaxToken, maxTokenValue)
	}

	return nil
}

func (p *PublicParams) String() string {
	res, err := json.MarshalIndent(p, " ", "  ")
	if err != nil {
		return err.Error()
	}

	return string(res)
}

func (p *PublicParams) Extras() driver.Extras {
	return p.ExtraData
}
