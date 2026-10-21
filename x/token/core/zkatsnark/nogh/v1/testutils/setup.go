/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"
	"os"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	nbenchmark "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
)

// Signer and OwnerIdentity are the generic x509/ECDSA and idemix(nym) identity
// helpers shared with the zkatdlog/nogh/v1 benchmark package: they carry no
// zkatdlog-specific logic (plain x509 certificate wrapping, idemix(nym) key
// management), so this package reuses them directly instead of duplicating
// them.
type (
	Signer        = nbenchmark.Signer
	OwnerIdentity = nbenchmark.OwnerIdentity
)

// PrepareECDSASigner and NewECDSASigner are re-exported from
// zkatdlog/nogh/v1/benchmark for the same reason as Signer/OwnerIdentity above.
var (
	PrepareECDSASigner = nbenchmark.PrepareECDSASigner
	NewECDSASigner     = nbenchmark.NewECDSASigner
)

// idemixCurveAndPath maps a zkatsnark outer (SNARK) curve to the mathlib
// curve ID and on-disk testdata subdirectory used for idemix(nym) owner
// identity material. This mapping exists only to pick which of the
// already-generated idemix fixtures to load; idemix's own group operations
// are entirely independent of the SNARK curve zkatsnark is configured with.
func idemixCurveAndPath(curve ecc.ID) (mathlib.CurveID, string, error) {
	switch curve {
	case ecc.BN254:
		return mathlib.BN254, "bn254", nil
	case ecc.BLS12_381:
		return mathlib.BLS12_381_BBS_GURVY, "bls12_381_bbs", nil
	default:
		return 0, "", errors.Errorf("testutils: unsupported curve %s", curve)
	}
}

// SetupConfiguration holds the prepared public parameters and related
// identities/signers/orchestrator used by a single benchmark/test
// configuration, for a single (zkatsnark outer curve, proof system) pair.
//
// Fields:
//   - PP: public parameters produced by setup.SetupAll.
//   - OwnerIdentity: identity information for the owner used in fixtures.
//   - AuditorSigner / IssuerSigner: ephemeral ECDSA signers for the auditor and issuer.
//   - SecondAuditorSigner: a second ephemeral ECDSA signer also registered as an
//     auditor in PP (PP.AuditorIdentities has 2 entries). Since AuditingSignaturesValidate
//     implements a 1-of-N policy, fixtures signed only by AuditorSigner remain valid;
//     fixtures that want to exercise the "signed by a non-first auditor key" path
//     should sign with this signer instead.
//   - Orchestrator: the gnark prover orchestrator built from PP's proving keys.
type SetupConfiguration struct {
	PP                  *pp.PublicParams
	OwnerIdentity       *OwnerIdentity
	AuditorSigner       *Signer
	SecondAuditorSigner *Signer
	IssuerSigner        *Signer
	Orchestrator        *prover.Orchestrator
	CurveID             ecc.ID
	ProofSystem         string
}

// SetupConfigurations contains a set of named benchmark/test configurations,
// one per supported (zkatsnark outer curve, proof system) pair.
type SetupConfigurations struct {
	Configurations map[string]*SetupConfiguration
}

// SetupParams holds all parameters needed to create benchmark/test
// configurations.
type SetupParams struct {
	// IdemixTestdataPath is the path to the idemix testdata directory shared
	// with zkatdlog/nogh/v1 (e.g. "../../../../token/core/zkatdlog/nogh/v1/testdata").
	IdemixTestdataPath string
	// CurveIDs lists the zkatsnark outer (SNARK) curves to build configurations for.
	CurveIDs []ecc.ID
	// OwnerIdentityType selects idemix or idemixnym owner identities.
	OwnerIdentityType identity.Type
	// ProofSystem selects the ZK proof system ("groth16" or "plonk"). Empty
	// defaults to pp.DefaultPublicParams's "groth16", preserving existing
	// callers' behavior.
	ProofSystem string
}

// NewSetupConfigurations loads test data from the given idemixTestdataPath and
// builds setup configurations for each of the provided curves, registering
// the issuer in the generated public parameters (closed issuer policy).
func NewSetupConfigurations(idemixTestdataPath string, curveIDs []ecc.ID, ownerIdentityType identity.Type) (*SetupConfigurations, error) {
	return newSetupConfigurationsWithParams(SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           curveIDs,
		OwnerIdentityType:  ownerIdentityType,
	}, true)
}

// NewSetupConfigurationsWithParams loads test data and builds setup
// configurations for each of the provided curves, registering the issuer in
// the generated public parameters (closed issuer policy).
func NewSetupConfigurationsWithParams(params SetupParams) (*SetupConfigurations, error) {
	return newSetupConfigurationsWithParams(params, true)
}

// NewOpenIssuerPolicySetupConfigurations mirrors NewSetupConfigurationsWithParams
// except that the generated PublicParams never registers the issuer
// (PP.IssuerIdentities stays empty), so IssueSignatureValidate's and
// TransferSignatureValidate's open-issuer-policy branches can be exercised
// end-to-end. An IssuerSigner is still generated and returned in each
// SetupConfiguration so fixtures can sign issue/redeem actions with an
// issuer identity that is deliberately not a member of PP.Issuers().
func NewOpenIssuerPolicySetupConfigurations(params SetupParams) (*SetupConfigurations, error) {
	return newSetupConfigurationsWithParams(params, false)
}

func newSetupConfigurationsWithParams(params SetupParams, registerIssuer bool) (*SetupConfigurations, error) {
	proofSystem := params.ProofSystem
	if proofSystem == "" {
		proofSystem = pp.DefaultPublicParams().ProofSystem
	}

	configurations := map[string]*SetupConfiguration{}

	for _, curveID := range params.CurveIDs {
		mathCurveID, idemixSubdir, err := idemixCurveAndPath(curveID)
		if err != nil {
			return nil, err
		}

		idemixPath := params.IdemixTestdataPath + "/" + idemixSubdir + "/idemix"
		oID, err := nbenchmark.LoadOwnerIdentityByType(context.Background(), idemixPath, mathCurveID, params.OwnerIdentityType)
		if err != nil {
			return nil, errors.Wrapf(err, "failed loading owner identity for curve [%s]", curveID)
		}

		idemixIssuerPK, err := os.ReadFile(idemixPath + "/msp/IssuerPublicKey")
		if err != nil {
			return nil, errors.Wrapf(err, "failed reading idemix issuer public key for curve [%s]", curveID)
		}

		auditorSigner, err := PrepareECDSASigner()
		if err != nil {
			return nil, err
		}
		secondAuditorSigner, err := PrepareECDSASigner()
		if err != nil {
			return nil, err
		}
		issuerSigner, err := PrepareECDSASigner()
		if err != nil {
			return nil, err
		}

		p := pp.DefaultPublicParams()
		p.Curve = curveID
		p.ProofSystem = proofSystem
		p.IdemixIssuerPublicKeys = []*pp.IdemixIssuerPublicKey{
			{PublicKey: idemixIssuerPK, Curve: mathCurveID},
		}
		p, err = setup.SetupAll(p)
		if err != nil {
			return nil, errors.Wrapf(err, "failed running trusted setup for curve [%s], proof system [%s]", curveID, proofSystem)
		}

		if registerIssuer {
			issuerID, err := issuerSigner.Serialize()
			if err != nil {
				return nil, err
			}
			p.AddIssuer(issuerID)
		}
		auditorID, err := auditorSigner.Serialize()
		if err != nil {
			return nil, err
		}
		p.AddAuditor(auditorID)
		secondAuditorID, err := secondAuditorSigner.Serialize()
		if err != nil {
			return nil, err
		}
		p.AddAuditor(secondAuditorID)

		orchestrator, err := newOrchestrator(p)
		if err != nil {
			return nil, errors.Wrapf(err, "failed building orchestrator for curve [%s], proof system [%s]", curveID, proofSystem)
		}

		configurations[key(curveID, proofSystem)] = &SetupConfiguration{
			PP:                  p,
			OwnerIdentity:       oID,
			AuditorSigner:       auditorSigner,
			SecondAuditorSigner: secondAuditorSigner,
			IssuerSigner:        issuerSigner,
			Orchestrator:        orchestrator,
			CurveID:             curveID,
			ProofSystem:         proofSystem,
		}
	}

	return &SetupConfigurations{Configurations: configurations}, nil
}

// newOrchestrator builds the gnark prover.Orchestrator for the given, already
// set-up public parameters (PKSpend/PKOutput populated by setup.SetupAll).
func newOrchestrator(p *pp.PublicParams) (*prover.Orchestrator, error) {
	spendCS, err := setup.CompileSpendCircuit(p)
	if err != nil {
		return nil, errors.Wrap(err, "failed compiling spend circuit")
	}
	spendPKBytes, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrap(err, "failed loading spend proving key")
	}

	outputCS, err := setup.CompileOutputCircuit(p)
	if err != nil {
		return nil, errors.Wrap(err, "failed compiling output circuit")
	}
	outputPKBytes, err := setup.LoadProvingKeyBytes(p, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrap(err, "failed loading output proving key")
	}

	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating proof system backend")
	}

	spendProver := prover.NewSpendProver(spendCS, spendPKBytes, b, p)
	outputProver := prover.NewOutputProver(outputCS, outputPKBytes, b, p)

	return prover.NewOrchestrator(spendProver, outputProver, p), nil
}

// GetSetupConfiguration returns the full SetupConfiguration for the provided
// curve, using the default proof system ("groth16"). It returns an error if
// the configuration is missing.
func (c *SetupConfigurations) GetSetupConfiguration(curveID ecc.ID) (*SetupConfiguration, error) {
	return c.GetSetupConfigurationFor(curveID, pp.DefaultPublicParams().ProofSystem)
}

// GetSetupConfigurationFor returns the full SetupConfiguration for the
// provided curve and proof system. It returns an error if the configuration
// is missing.
func (c *SetupConfigurations) GetSetupConfigurationFor(curveID ecc.ID, proofSystem string) (*SetupConfiguration, error) {
	configuration, ok := c.Configurations[key(curveID, proofSystem)]
	if !ok {
		return nil, errors.New("configuration not found")
	}

	return configuration, nil
}

func key(curveID ecc.ID, proofSystem string) string {
	return curveID.String() + "/" + proofSystem
}
