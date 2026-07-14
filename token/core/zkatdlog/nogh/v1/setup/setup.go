/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"crypto/sha256"
	math3 "math"
	"math/big"
	"math/bits"
	"strconv"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	pp3 "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	utils2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/csp"
	executor "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/executor"
	"github.com/LFDT-Panurus/panurus/token/driver"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	pp2 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

const (
	DLogNoGHDriverName = "zkatdlognogh"
	ProtocolV1         = driver.TokenDriverVersion(1)
)

var SupportedPrecisions = []uint64{16, 32, 64}

// SetupParams contains all parameters needed to setup public parameters
type SetupParams struct {
	DriverName     driver.TokenDriverName
	DriverVersion  driver.TokenDriverVersion
	BitLength      uint64
	IdemixIssuerPK []byte
	CurveID        mathlib.CurveID
	ProofType      rp.ProofType
}

type CSPRangeProofParams struct {
	LeftGenerators     []*mathlib.G1
	RightGenerators    []*mathlib.G1
	BitLength          uint64
	RPTranscriptHeader []byte
}

func (rpp *CSPRangeProofParams) Validate(curveID mathlib.CurveID) error {
	if rpp.BitLength == 0 {
		return errors.New("invalid range proof parameters: bit length is zero")
	}
	if len(rpp.LeftGenerators) != len(rpp.RightGenerators) {
		return errors.Errorf("invalid range proof parameters: the size of the left generators does not match the size of the right generators [%d vs, %d]", len(rpp.LeftGenerators), len(rpp.RightGenerators))
	}
	if err := math.CheckElements(rpp.LeftGenerators, curveID, rpp.BitLength+1); err != nil {
		return errors.Wrap(err, "invalid range proof parameters, left generators is invalid")
	}
	if err := math.CheckElements(rpp.RightGenerators, curveID, rpp.BitLength+1); err != nil {
		return errors.Wrap(err, "invalid range proof parameters, right generators is invalid")
	}

	return nil
}

func (rpp *CSPRangeProofParams) ToProtos() (*pp.CSPRangeProofParams, error) {
	lefGenerators, err := utils2.ToProtoG1Slice(rpp.LeftGenerators)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert left generators to protos")
	}
	rightGenerators, err := utils2.ToProtoG1Slice(rpp.RightGenerators)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert right generators to protos")
	}
	rangeProofParams := &pp.CSPRangeProofParams{
		LeftGenerators:  lefGenerators,
		RightGenerators: rightGenerators,
		BitLength:       rpp.BitLength,
	}

	return rangeProofParams, nil
}

func (rpp *CSPRangeProofParams) FromProto(params *pp.CSPRangeProofParams) error {
	if params == nil {
		return nil
	}
	rpp.BitLength = params.BitLength
	var err error
	rpp.LeftGenerators, err = utils2.FromG1ProtoSlice(params.LeftGenerators)
	if err != nil {
		return errors.Wrapf(err, "failed to convert left generators to protos")
	}
	rpp.RightGenerators, err = utils2.FromG1ProtoSlice(params.RightGenerators)
	if err != nil {
		return errors.Wrapf(err, "failed to convert right generators to protos")
	}

	return nil
}

type RangeProofParams struct {
	LeftGenerators  []*mathlib.G1
	RightGenerators []*mathlib.G1
	P               *mathlib.G1
	Q               *mathlib.G1
	BitLength       uint64
	NumberOfRounds  uint64
}

func (rpp *RangeProofParams) Validate(curveID mathlib.CurveID) error {
	if rpp.BitLength == 0 {
		return errors.New("invalid range proof parameters: bit length is zero")
	}
	if rpp.NumberOfRounds == 0 {
		return errors.New("invalid range proof parameters: number of rounds is zero")
	}
	if rpp.NumberOfRounds > 64 {
		return errors.New("invalid range proof parameters: number of rounds must be smaller or equal to 64")
	}
	if rpp.BitLength != uint64(1<<rpp.NumberOfRounds) {
		return errors.Errorf("invalid range proof parameters: bit length should be %d\n", uint64(1<<rpp.NumberOfRounds))
	}
	if len(rpp.LeftGenerators) != len(rpp.RightGenerators) {
		return errors.Errorf("invalid range proof parameters: the size of the left generators does not match the size of the right generators [%d vs, %d]", len(rpp.LeftGenerators), len(rpp.RightGenerators))
	}
	if err := math.CheckElement(rpp.Q, curveID); err != nil {
		return errors.Wrapf(err, "invalid range proof parameters: generator Q is invalid")
	}
	if err := math.CheckElement(rpp.P, curveID); err != nil {
		return errors.Wrapf(err, "invalid range proof parameters: generator P is invalid")
	}
	if err := math.CheckElements(rpp.LeftGenerators, curveID, rpp.BitLength); err != nil {
		return errors.Wrap(err, "invalid range proof parameters, left generators is invalid")
	}
	if err := math.CheckElements(rpp.RightGenerators, curveID, rpp.BitLength); err != nil {
		return errors.Wrap(err, "invalid range proof parameters, right generators is invalid")
	}

	return nil
}

func (rpp *RangeProofParams) ToProtos() (*pp.RangeProofParams, error) {
	lefGenerators, err := utils2.ToProtoG1Slice(rpp.LeftGenerators)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert left generators to protos")
	}
	rightGenerators, err := utils2.ToProtoG1Slice(rpp.RightGenerators)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert right generators to protos")
	}
	p, err := utils2.ToProtoG1(rpp.P)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert p to proto")
	}
	q, err := utils2.ToProtoG1(rpp.Q)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert q to proto")
	}

	rangeProofParams := &pp.RangeProofParams{
		LeftGenerators:  lefGenerators,
		RightGenerators: rightGenerators,
		P:               p,
		Q:               q,
		BitLength:       rpp.BitLength,
		NumberOfRounds:  rpp.NumberOfRounds,
	}

	return rangeProofParams, nil
}

func (rpp *RangeProofParams) FromProto(params *pp.RangeProofParams) error {
	rpp.NumberOfRounds = params.NumberOfRounds
	rpp.BitLength = params.BitLength
	var err error
	rpp.LeftGenerators, err = utils2.FromG1ProtoSlice(params.LeftGenerators)
	if err != nil {
		return errors.Wrapf(err, "failed to convert left generators to protos")
	}
	rpp.RightGenerators, err = utils2.FromG1ProtoSlice(params.RightGenerators)
	if err != nil {
		return errors.Wrapf(err, "failed to convert right generators to protos")
	}
	rpp.P, err = utils2.FromG1Proto(params.P)
	if err != nil {
		return errors.Wrapf(err, "failed to convert p to proto")
	}
	rpp.Q, err = utils2.FromG1Proto(params.Q)
	if err != nil {
		return errors.Wrapf(err, "failed to convert q to proto")
	}

	return nil
}

type IdemixIssuerPublicKey struct {
	PublicKey []byte
	Curve     mathlib.CurveID
}

func (i *IdemixIssuerPublicKey) ToProtos() (*pp.IdemixIssuerPublicKey, error) {
	if i.Curve < 0 {
		return nil, errors.New("invalid curve id")
	}

	return &pp.IdemixIssuerPublicKey{
		PublicKey: i.PublicKey,
		CurveId: &math2.CurveID{
			Id: uint64(i.Curve), // #nosec G115
		},
	}, nil
}

func (i *IdemixIssuerPublicKey) FromProtos(s *pp.IdemixIssuerPublicKey) error {
	if s.PublicKey == nil {
		return errors.New("invalid idemix public key, it is nil")
	}
	i.PublicKey = s.PublicKey
	if s.CurveId == nil {
		return errors.New("invalid idemix issuer public key")
	}
	if s.CurveId.Id > math3.MaxInt {
		return errors.New("curve id out of range")
	}
	i.Curve = mathlib.CurveID(s.CurveId.Id) // #nosec G115

	return nil
}

type PublicParams struct {
	// DriverName is the name of token driver this public params refer to.
	DriverName driver.TokenDriverName
	// DriverVersion is the version of token driver this public params refer to.
	DriverVersion driver.TokenDriverVersion
	// Curve is the pairing-friendly elliptic curve used for everything but Idemix.
	Curve mathlib.CurveID
	// PedersenGenerators contains the public parameters for the Pedersen commitment scheme.
	PedersenGenerators []*mathlib.G1
	// RangeProofParams contains the public parameters for the range proof scheme.
	RangeProofParams *RangeProofParams
	// IdemixIssuerPublicKeys contains the idemix issuer public keys
	// Wallets should prefer the use of keys valid under the public key whose index in the array is the smallest.
	IdemixIssuerPublicKeys []*IdemixIssuerPublicKey
	// Auditors is a list of the public keys of the auditor(s).
	AuditorIDs []driver.Identity
	// IssuerIDs is a list of public keys of the entities that can issue tokens.
	IssuerIDs []driver.Identity
	// MaxToken is the maximum quantity a token can hold
	MaxToken uint64
	// QuantityPrecision is the precision used to represent quantities
	QuantityPrecision uint64
	// ExtraData contains any extra custom data
	ExtraData           driver.Extras
	CSPRangeProofParams *CSPRangeProofParams
	// ExecutorProvider is a runtime-only field (not serialized) that controls
	// how independent range proofs are executed. If nil, SerialProvider is used.
	ExecutorProvider executor.ExecutorProvider `json:"-"`
}

// NewPublicParamsFromBytes unmarshal the given serialized version of the public parameters
// for the given driver name and version.
// It is responsibility of the caller to validate the public parameters before using them.
func NewPublicParamsFromBytes(
	raw []byte,
	driverName driver.TokenDriverName,
	driverVersion driver.TokenDriverVersion,
) (*PublicParams, error) {
	pp := &PublicParams{}
	pp.DriverName = driverName
	pp.DriverVersion = driverVersion
	if err := pp.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed parsing public parameters")
	}

	return pp, nil
}

func Setup(bitLength uint64, idemixIssuerPK []byte, curveID mathlib.CurveID) (*PublicParams, error) {
	return NewWith(SetupParams{
		DriverName:     DLogNoGHDriverName,
		DriverVersion:  ProtocolV1,
		BitLength:      bitLength,
		IdemixIssuerPK: idemixIssuerPK,
		CurveID:        curveID,
		ProofType:      rp.RangeProofType,
	})
}

// WithVersion is like Setup with the additional possibility to specify the version number
func WithVersion(bitLength uint64, idemixIssuerPK []byte, curveID mathlib.CurveID, version driver.TokenDriverVersion) (*PublicParams, error) {
	return NewWith(SetupParams{
		DriverName:     DLogNoGHDriverName,
		DriverVersion:  version,
		BitLength:      bitLength,
		IdemixIssuerPK: idemixIssuerPK,
		CurveID:        curveID,
		ProofType:      rp.RangeProofType,
	})
}

func NewWith(params SetupParams) (*PublicParams, error) {
	if params.BitLength > 64 {
		return nil, errors.Errorf("invalid bit length [%d], should be smaller than 64", params.BitLength)
	}
	if params.BitLength == 0 {
		return nil, errors.New("invalid bit length, should be greater than 0")
	}
	pp := &PublicParams{
		DriverName:    params.DriverName,
		DriverVersion: params.DriverVersion,
		Curve:         params.CurveID,
		IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{
			{
				PublicKey: params.IdemixIssuerPK,
				Curve:     params.CurveID,
			},
		},
		QuantityPrecision: params.BitLength,
		ExtraData:         driver.Extras{},
	}
	if err := pp.GeneratePedersenParameters(); err != nil {
		return nil, errors.Wrapf(err, "failed to generated pedersen parameters")
	}

	// Generate range proof parameters based on proof type
	switch params.ProofType {
	case rp.RangeProofType:
		if err := pp.GenerateRangeProofParameters(params.BitLength); err != nil {
			return nil, errors.Wrapf(err, "failed to generated range-proof parameters")
		}
	case rp.CSPRangeProofType:
		if err := pp.GenerateCSPRangeProofParameters(params.BitLength); err != nil {
			return nil, errors.Wrapf(err, "failed to generated CSP range-proof parameters")
		}
	default:
		return nil, errors.Errorf("unrecognized range proof type: %d", params.ProofType)
	}

	pp.MaxToken = pp.ComputeMaxTokenValue()

	return pp, nil
}

func (p *PublicParams) TokenDriverName() driver.TokenDriverName {
	return p.DriverName
}

func (p *PublicParams) TokenDriverVersion() driver.TokenDriverVersion {
	return p.DriverVersion
}

func (p *PublicParams) CertificationDriver() string {
	return string(p.DriverName)
}

func (p *PublicParams) TokenDataHiding() bool {
	return true
}

func (p *PublicParams) GraphHiding() bool {
	return false
}

func (p *PublicParams) MaxTokenValue() uint64 {
	return p.MaxToken
}

func (p *PublicParams) Bytes() ([]byte, error) {
	return p.Serialize()
}

func (p *PublicParams) Auditors() []driver.Identity {
	return p.AuditorIDs
}

// Issuers returns the list of authorized issuers
func (p *PublicParams) Issuers() []driver.Identity {
	return p.IssuerIDs
}

func (p *PublicParams) Precision() uint64 {
	return p.QuantityPrecision
}

func (p *PublicParams) Serialize() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed to serialize public parameters")
	}
	pg, err := utils2.ToProtoG1Slice(p.PedersenGenerators)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize public parameters")
	}
	var rpp *pp.RangeProofParams
	if p.RangeProofParams != nil {
		rpp, err = p.RangeProofParams.ToProtos()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize range proof parameters")
		}
	}
	var cspRPP *pp.CSPRangeProofParams
	if p.CSPRangeProofParams != nil {
		cspRPP, err = p.CSPRangeProofParams.ToProtos()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize csp-based range proof parameters")
		}
	}
	issuers, err := protos.ToProtosSliceFunc(p.IssuerIDs, func(id driver.Identity) (*protosv1.Identity, error) {
		return &protosv1.Identity{
			Raw: id,
		}, nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize issuer")
	}
	auditors, err := protos.ToProtosSliceFunc(p.AuditorIDs, func(id driver.Identity) (*protosv1.Identity, error) {
		return &protosv1.Identity{
			Raw: id,
		}, nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize auditor")
	}
	idemixIssuerPublicKeys, err := protos.ToProtosSlice[pp.IdemixIssuerPublicKey, *IdemixIssuerPublicKey](p.IdemixIssuerPublicKeys)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize idemix issuer public keys")
	}

	publicParams := &pp.PublicParameters{
		TokenDriverName:    string(p.DriverName),
		TokenDriverVersion: uint32(p.DriverVersion),
		CurveId: &math2.CurveID{
			Id: uint64(p.Curve), // #nosec G115
		},
		PedersenGenerators:     pg,
		RangeProofParams:       rpp,
		CspRangeProofParams:    cspRPP,
		IdemixIssuerPublicKeys: idemixIssuerPublicKeys,
		Auditors:               auditors,
		Issuers:                issuers,
		MaxToken:               p.MaxToken,
		QuantityPrecision:      p.QuantityPrecision,
		Metadata:               p.ExtraData,
	}
	raw, err := proto.Marshal(publicParams)
	if err != nil {
		return nil, err
	}

	return pp3.Marshal(&pp2.PublicParameters{
		Identifier: string(core.DriverIdentifier(p.DriverName, p.DriverVersion)),
		Raw:        raw,
	})
}

func (p *PublicParams) Deserialize(raw []byte) error {
	container, err := pp3.Unmarshal(raw)
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

	publicParams := &pp.PublicParameters{}
	if err := proto.Unmarshal(container.Raw, publicParams); err != nil {
		return errors.Wrapf(err, "failed unmarshalling public parameters")
	}

	p.DriverName = driver.TokenDriverName(publicParams.TokenDriverName)
	p.DriverVersion = driver.TokenDriverVersion(publicParams.TokenDriverVersion)
	if publicParams.CurveId == nil {
		return errors.Errorf("invalid curve id, expecting curve id, got nil")
	}
	if publicParams.CurveId.Id > math3.MaxInt {
		return errors.New("curve id out of range")
	}
	p.Curve = mathlib.CurveID(publicParams.CurveId.Id) // #nosec G115
	p.MaxToken = publicParams.MaxToken
	p.QuantityPrecision = publicParams.QuantityPrecision
	pg, err := utils2.FromG1ProtoSlice(publicParams.PedersenGenerators)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize public parameters")
	}
	p.PedersenGenerators = pg
	issuers, err := protos.FromProtosSliceFunc2(publicParams.Issuers, func(id *protosv1.Identity) (driver.Identity, error) {
		if id == nil {
			return nil, nil
		}

		return id.Raw, nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize issuers")
	}
	p.IssuerIDs = issuers
	auditors, err := protos.FromProtosSliceFunc2(publicParams.Auditors, func(id *protosv1.Identity) (driver.Identity, error) {
		if id == nil {
			return nil, nil
		}

		return id.Raw, nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize issuers")
	}
	p.AuditorIDs = auditors

	p.IdemixIssuerPublicKeys = slices.GenericSliceOfPointers[IdemixIssuerPublicKey](len(publicParams.IdemixIssuerPublicKeys))
	err = protos.FromProtosSlice[pp.IdemixIssuerPublicKey, *IdemixIssuerPublicKey](publicParams.IdemixIssuerPublicKeys, p.IdemixIssuerPublicKeys)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize idemix issuer public keys")
	}

	if publicParams.GetRangeProofParams() != nil {
		p.RangeProofParams = &RangeProofParams{}
		if err := p.RangeProofParams.FromProto(publicParams.GetRangeProofParams()); err != nil {
			return errors.Wrapf(err, "failed to deserialize range proof parameters")
		}
	}

	if publicParams.GetCspRangeProofParams() != nil {
		p.CSPRangeProofParams = &CSPRangeProofParams{}
		if err := p.CSPRangeProofParams.FromProto(publicParams.GetCspRangeProofParams()); err != nil {
			return errors.Wrapf(err, "failed to deserialize csp range proof parameters")
		}
	}

	p.ExtraData = publicParams.Metadata
	if p.ExtraData == nil {
		p.ExtraData = driver.Extras{}
	}

	return nil
}

func (p *PublicParams) GeneratePedersenParameters() error {
	curve := mathlib.Curves[p.Curve]
	p.PedersenGenerators = make([]*mathlib.G1, 3)

	for i := 0; i < len(p.PedersenGenerators); i++ {
		p.PedersenGenerators[i] = curve.HashToG1([]byte("lfdt-panurus." + string(p.DriverName) + "." + strconv.Itoa(int(p.DriverVersion)) + ".PedersenGenerators." + strconv.Itoa(i)))
	}

	return nil
}

func (p *PublicParams) GenerateRangeProofParameters(bitLength uint64) error {
	curve := mathlib.Curves[p.Curve]

	domainPrefix := "lfdt-panurus." + string(p.DriverName) + "." + strconv.Itoa(int(p.DriverVersion)) + "."
	p.RangeProofParams = &RangeProofParams{
		P:              curve.HashToG1([]byte(domainPrefix + "RangeProof.P")),
		Q:              curve.HashToG1([]byte(domainPrefix + "RangeProof.Q")),
		BitLength:      bitLength,
		NumberOfRounds: log2(bitLength),
	}
	p.RangeProofParams.LeftGenerators = make([]*mathlib.G1, bitLength)
	p.RangeProofParams.RightGenerators = make([]*mathlib.G1, bitLength)

	for i := range bitLength {
		p.RangeProofParams.LeftGenerators[i] = curve.HashToG1([]byte(domainPrefix + "RangeProof.L." + strconv.FormatUint(i, 10)))
		p.RangeProofParams.RightGenerators[i] = curve.HashToG1([]byte(domainPrefix + "RangeProof.R." + strconv.FormatUint(i, 10)))
	}

	return nil
}

func (p *PublicParams) GenerateCSPRangeProofParameters(bitLength uint64) error {
	curve := mathlib.Curves[p.Curve]

	domainPrefix := "lfdt-panurus." + string(p.DriverName) + "." + strconv.Itoa(int(p.DriverVersion)) + "."
	p.CSPRangeProofParams = &CSPRangeProofParams{
		BitLength: bitLength,
	}
	p.CSPRangeProofParams.LeftGenerators = make([]*mathlib.G1, bitLength+1)
	p.CSPRangeProofParams.RightGenerators = make([]*mathlib.G1, bitLength+1)

	for i := range bitLength + 1 {
		p.CSPRangeProofParams.LeftGenerators[i] = curve.HashToG1([]byte(domainPrefix + "CSPRangeProof.L." + strconv.FormatUint(i, 10)))
		p.CSPRangeProofParams.RightGenerators[i] = curve.HashToG1([]byte(domainPrefix + "CSPRangeProof.R." + strconv.FormatUint(i, 10)))
	}

	return nil
}

func (p *PublicParams) AddAuditor(auditor driver.Identity) {
	p.AuditorIDs = append(p.AuditorIDs, auditor)
}

func (p *PublicParams) AddIssuer(id driver.Identity) {
	p.IssuerIDs = append(p.IssuerIDs, id)
}

// SetIssuers sets the issuers to the passed identities
func (p *PublicParams) SetIssuers(ids []driver.Identity) {
	p.IssuerIDs = ids
}

// SetAuditors sets the auditors to the passed identities
func (p *PublicParams) SetAuditors(ids []driver.Identity) {
	p.AuditorIDs = ids
}

func (p *PublicParams) ComputeHash() ([]byte, error) {
	raw, err := p.Bytes()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to serialize public params")
	}
	hash := sha256.New()
	n, err := hash.Write(raw)
	if n != len(raw) {
		return nil, errors.New("failed to hash public parameters")
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to hash public parameters")
	}

	return hash.Sum(nil), nil
}

func (p *PublicParams) ComputeMaxTokenValue() uint64 {
	if p.RangeProofParams != nil {
		return 1<<p.RangeProofParams.BitLength - 1
	}
	if p.CSPRangeProofParams != nil {
		tr := csp.Transcript{Curve: mathlib.Curves[p.Curve]}
		tr.InitHasher()
		// Absorb the public statement: PedersenGenerators || LeftGenerators || RightGenerators || NumberOfBits.
		for _, g := range p.PedersenGenerators {
			tr.Absorb(g.Bytes())
		}
		// p.CSPRangeProofParams.RPTranscriptHeader = tr.State()
		for _, g := range p.CSPRangeProofParams.LeftGenerators {
			tr.Absorb(g.Bytes())
		}
		for _, g := range p.CSPRangeProofParams.RightGenerators {
			tr.Absorb(g.Bytes())
		}
		n := p.CSPRangeProofParams.BitLength
		tr.Absorb(new(big.Int).SetUint64(n).Bytes())
		p.CSPRangeProofParams.RPTranscriptHeader = tr.State()

		return 1<<p.CSPRangeProofParams.BitLength - 1
	}

	return 0
}

func (p *PublicParams) String() string {
	res, err := json.MarshalIndent(p, " ", "  ")
	if err != nil {
		return err.Error()
	}

	return string(res)
}

// Validate validates the public parameters.
// The list of issuers can be empty meaning that anyone can create tokens.
func (p *PublicParams) Validate() error {
	if int(p.Curve) > len(mathlib.Curves)-1 {
		return errors.Errorf("invalid public parameters: invalid curveID [%d > %d]", int(p.Curve), len(mathlib.Curves)-1)
	}
	if len(p.IdemixIssuerPublicKeys) == 0 {
		return errors.Errorf("expected at least one idemix issuer public key, found [%d]", len(p.IdemixIssuerPublicKeys))
	}

	for _, issuer := range p.IdemixIssuerPublicKeys {
		if issuer == nil {
			return errors.Errorf("invalid idemix issuer public key, it is nil")
		}
		if len(issuer.PublicKey) == 0 {
			return errors.Errorf("expected idemix issuer public key to be non-empty")
		}
		if int(issuer.Curve) > len(mathlib.Curves)-1 {
			return errors.Errorf("invalid public parameters: invalid idemix curveID [%d > %d]", int(p.Curve), len(mathlib.Curves)-1)
		}
	}
	if err := math.CheckElements(p.PedersenGenerators, p.Curve, 3); err != nil {
		return errors.Wrapf(err, "invalid pedersen generators")
	}

	if p.RangeProofParams == nil && p.CSPRangeProofParams == nil {
		return errors.New("invalid public parameters: nil range proof parameters")
	}

	if p.RangeProofParams != nil {
		bitLength := p.RangeProofParams.BitLength
		supportedPrecisions := collections.NewSet(SupportedPrecisions...)
		if !supportedPrecisions.Contains(bitLength) {
			return errors.Errorf("invalid bit length [%d], should be one of [%v]", bitLength, supportedPrecisions.ToSlice())
		}
		err := p.RangeProofParams.Validate(p.Curve)
		if err != nil {
			return errors.Wrap(err, "invalid public parameters")
		}
		if p.QuantityPrecision != p.RangeProofParams.BitLength {
			return errors.Errorf("invalid public parameters: quantity precision should be [%d] instead it is [%d]", p.RangeProofParams.BitLength, p.QuantityPrecision)
		}
	}

	if p.CSPRangeProofParams != nil {
		bitLength := p.CSPRangeProofParams.BitLength
		supportedPrecisions := collections.NewSet(SupportedPrecisions...)
		if !supportedPrecisions.Contains(bitLength) {
			return errors.Errorf("invalid bit length [%d], should be one of [%v]", bitLength, supportedPrecisions.ToSlice())
		}
		err := p.CSPRangeProofParams.Validate(p.Curve)
		if err != nil {
			return errors.Wrap(err, "invalid public parameters")
		}
		if p.QuantityPrecision != p.CSPRangeProofParams.BitLength {
			return errors.Errorf("invalid public parameters: quantity precision should be [%d] instead it is [%d]", p.CSPRangeProofParams.BitLength, p.QuantityPrecision)
		}
	}

	maxToken := p.ComputeMaxTokenValue()
	if maxToken != p.MaxToken {
		return errors.Errorf("invalid max token, [%d]!=[%d]", maxToken, p.MaxToken)
	}

	return nil
}

// ValidateForDeployment checks configuration choices that are technically valid
// but may indicate a misconfiguration. It does not change the behaviour of
// Validate(); empty IssuerIDs remain permitted (open-policy mode).
//
// Returns a non-nil error if the public parameters appear to be in open-policy
// mode (IssuerIDs is empty), so that callers can surface an explicit warning at
// deployment time instead of silently operating in a mode where any identity
// can mint or redeem tokens.
//
// See FA-1 in the security analysis for the rationale.
func (p *PublicParams) ValidateForDeployment() error {
	if len(p.IssuerIDs) == 0 {
		return errors.New("open-policy mode: IssuerIDs is empty — any identity can issue and redeem tokens; populate IssuerIDs to restrict minting and redemption")
	}

	return nil
}

func (p *PublicParams) Extras() driver.Extras {
	return p.ExtraData
}

func (p *PublicParams) BitLength() uint64 {
	if p.RangeProofParams != nil {
		return p.RangeProofParams.BitLength
	}

	return p.CSPRangeProofParams.BitLength
}

// SupportsRangeProofType reports whether the params sub-struct required by
// proofType is populated. Both sub-structs may be present simultaneously (e.g.
// during a range-proof-algorithm migration), so this is a per-type availability
// check rather than a single-value getter.
func (p *PublicParams) SupportsRangeProofType(proofType rp.ProofType) bool {
	switch proofType {
	case rp.RangeProofType:
		return p.RangeProofParams != nil
	case rp.CSPRangeProofType:
		return p.CSPRangeProofParams != nil
	default:
		return false
	}
}

func log2(x uint64) uint64 {
	if x == 0 {
		return 0
	}

	return uint64(bits.Len64(x)) - 1 //nolint:gosec
}
