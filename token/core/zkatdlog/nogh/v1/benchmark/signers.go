/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/executor"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	crypto2 "github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	paramsFile  = "params.txt"
	ppFile      = "pp.json"
	signersFile = "signers.json"
)

// SignerSer is the serialized form of a Signer. All fields are base64 encoded.
// The key is a DER-encoded EC private key. These are test-only keys: they are
// stored in clear next to the public parameters they are registered in.
type SignerSer struct {
	SK        string `json:"sk"`
	ID        string `json:"id"`
	AuditInfo string `json:"audit_info"`
}

// SignersSer is the content of signers.json.
type SignersSer struct {
	Auditor       SignerSer `json:"auditor"`
	SecondAuditor SignerSer `json:"second_auditor"`
	Issuer        SignerSer `json:"issuer"`
}

func serializeSigner(s *Signer) (SignerSer, error) {
	if s == nil || s.SK == nil {
		return SignerSer{}, errors.New("nil signer")
	}
	der, err := x509.MarshalECPrivateKey(s.SK)
	if err != nil {
		return SignerSer{}, errors.Wrap(err, "failed marshalling private key")
	}

	return SignerSer{
		SK:        base64.StdEncoding.EncodeToString(der),
		ID:        base64.StdEncoding.EncodeToString(s.ID),
		AuditInfo: base64.StdEncoding.EncodeToString(s.AuditInfo),
	}, nil
}

func deserializeSigner(s SignerSer) (*Signer, error) {
	der, err := base64.StdEncoding.DecodeString(s.SK)
	if err != nil {
		return nil, errors.Wrap(err, "failed decoding private key")
	}
	sk, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, errors.Wrap(err, "failed parsing private key")
	}
	id, err := base64.StdEncoding.DecodeString(s.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed decoding identity")
	}
	auditInfo, err := base64.StdEncoding.DecodeString(s.AuditInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed decoding audit info")
	}

	return &Signer{SK: sk, Signer: crypto2.NewEcdsaSigner(sk), ID: id, AuditInfo: auditInfo}, nil
}

// SaveWithSignersTo behaves as SaveTo and additionally writes, next to params.txt
// and pp.json, a signers.json file with the auditor, second auditor, and issuer
// signers, so that new requests can be signed against the stored parameters.
// The private keys are test-only.
func (c *SetupConfigurations) SaveWithSignersTo(dir string) error {
	if err := c.SaveTo(dir); err != nil {
		return err
	}
	for k, cfg := range c.Configurations {
		var (
			ser SignersSer
			err error
		)
		if ser.Auditor, err = serializeSigner(cfg.AuditorSigner); err != nil {
			return errors.WithMessagef(err, "auditor signer of [%s]", k)
		}
		if ser.SecondAuditor, err = serializeSigner(cfg.SecondAuditorSigner); err != nil {
			return errors.WithMessagef(err, "second auditor signer of [%s]", k)
		}
		if ser.Issuer, err = serializeSigner(cfg.IssuerSigner); err != nil {
			return errors.WithMessagef(err, "issuer signer of [%s]", k)
		}
		data, err := json.MarshalIndent(ser, "", "  ")
		if err != nil {
			return errors.Wrap(err, "failed marshalling signers")
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(k), signersFile), data, 0o600); err != nil {
			return errors.WithMessagef(err, "failed writing signers for key: %s", k)
		}
	}

	return nil
}

// LoadSetupConfigurations loads the configuration stored in dir of fsys by
// SaveWithSignersTo: params.txt, pp.json, and signers.json. The owner identity
// is loaded as for a fresh configuration, from params.IdemixTestdataPath and
// params.OwnerIdentityType. params.ExecutorProvider selects the range-proof
// executor. The result contains a single configuration, keyed by its bits and curve.
func LoadSetupConfigurations(fsys fs.FS, dir string, params SetupParams) (*SetupConfigurations, error) {
	paramsData, err := fs.ReadFile(fsys, path.Join(dir, paramsFile))
	if err != nil {
		return nil, errors.Wrapf(err, "failed reading [%s]", paramsFile)
	}
	raw, err := base64.StdEncoding.DecodeString(string(paramsData))
	if err != nil {
		return nil, errors.Wrapf(err, "failed decoding [%s]", paramsFile)
	}
	pp, err := setup.NewPublicParamsFromBytes(raw, setup.DLogNoGHDriverName, setup.ProtocolV1)
	if err != nil {
		return nil, errors.Wrap(err, "failed deserializing public parameters")
	}

	metaData, err := fs.ReadFile(fsys, path.Join(dir, ppFile))
	if err != nil {
		return nil, errors.Wrapf(err, "failed reading [%s]", ppFile)
	}
	meta := &SetupConfigurationSer{}
	if err := json.Unmarshal(metaData, meta); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling [%s]", ppFile)
	}
	curveID := math.CurveID(meta.CurveID)
	if curveID != pp.Curve || meta.Bits != pp.BitLength() {
		return nil, errors.Errorf("[%s] does not match the public parameters: bits [%d] curve [%d], expected bits [%d] curve [%d]",
			ppFile, meta.Bits, curveID, pp.BitLength(), pp.Curve)
	}

	signersData, err := fs.ReadFile(fsys, path.Join(dir, signersFile))
	if err != nil {
		return nil, errors.Wrapf(err, "failed reading [%s]", signersFile)
	}
	ser := &SignersSer{}
	if err := json.Unmarshal(signersData, ser); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling [%s]", signersFile)
	}
	auditor, err := deserializeSigner(ser.Auditor)
	if err != nil {
		return nil, errors.WithMessage(err, "auditor signer")
	}
	secondAuditor, err := deserializeSigner(ser.SecondAuditor)
	if err != nil {
		return nil, errors.WithMessage(err, "second auditor signer")
	}
	issuer, err := deserializeSigner(ser.Issuer)
	if err != nil {
		return nil, errors.WithMessage(err, "issuer signer")
	}

	_, oID, err := loadCurveIssuerAndOwner(params, curveID)
	if err != nil {
		return nil, err
	}

	provider := params.ExecutorProvider
	if provider == nil {
		provider = executor.SerialProvider{}
	}
	pp.ExecutorProvider = provider

	return &SetupConfigurations{Configurations: map[string]*SetupConfiguration{
		key(meta.Bits, curveID): {
			Bits:                meta.Bits,
			CurveID:             curveID,
			PP:                  pp,
			OwnerIdentity:       oID,
			AuditorSigner:       auditor,
			SecondAuditorSigner: secondAuditor,
			IssuerSigner:        issuer,
			ExecutorProvider:    provider,
		},
	}}, nil
}
