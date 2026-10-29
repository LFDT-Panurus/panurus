/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package zkatsnarkv1 implements the tokengen commands that generate and
// update public parameters for the ZKAT-SNARK (zkatsnark.v1) token driver.
package zkatsnarkv1

import (
	"fmt"
	"os"
	"path/filepath"

	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/idemix"
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

const (
	// DriverName is the name of the ZKAT-SNARK token driver.
	DriverName = "zkatsnark"
	// DefaultProofSystem is the proof system used when none is specified.
	DefaultProofSystem = setup.ProofSystemGroth16
	// DefaultCurve is the SNARK curve used when none is specified.
	DefaultCurve = "bls12_381"
)

// DriverIdentifier is the identifier of the ZKAT-SNARK v1 token driver (zkatsnark.v1).
var DriverIdentifier = string(core.DriverIdentifier(DriverName, 1))

// GeneratorArgs defines the arguments for the ZKAT-SNARK public parameters generator.
type GeneratorArgs struct {
	// IdemixMSPDirs lists the directories containing the Idemix MSP config (Issuer Public Key).
	// At least one is required; wallets prefer keys earlier in the list.
	IdemixMSPDirs []string
	// OutputDir is the directory to output the generated files
	OutputDir string
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate
	Auditors []string
	// ProofSystem is one of groth16, plonk, plonkfri. Empty means groth16.
	ProofSystem string
	// Curve is the SNARK curve, bn254 or bls12_381. Empty means bls12_381.
	Curve string
	// MaxBits bounds the token value range inside the circuits (1..64).
	MaxBits uint64
	// Precision is the token quantity precision. Zero means MaxBits. It must not exceed MaxBits.
	Precision uint64
	// Aries is a flag to indicate that aries should be used as backend for idemix
	Aries bool
	// Version allows the caller of tokengen to override the version number put in the public params
	Version uint32
	// Extras lists extra data entries in key=path format
	Extras []string
}

var genArgs GeneratorArgs

// Cmd returns the Cobra Command for ZKAT-SNARK public parameters generation.
func Cmd() *cobra.Command {
	genArgs = GeneratorArgs{}
	c := &cobra.Command{
		Use:   DriverIdentifier,
		Short: "Gen ZKAT-SNARK public parameters.",
		Long:  `Generates ZKAT-SNARK public parameters, including the circuit proving and verification keys.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errors.New("trailing args detected")
			}
			// Parsing of the command line is done so silence cmd usage
			cmd.SilenceUsage = true
			if _, err := Gen(&genArgs); err != nil {
				fmt.Printf("failed to generate public parameters [%s]\n", err)

				return errors.Wrap(err, "failed to generate public parameters")
			}

			return nil
		},
	}
	flags := c.Flags()
	flags.StringVarP(&genArgs.OutputDir, "output", "o", ".", "output folder")
	flags.StringSliceVarP(&genArgs.Auditors, "auditors", "a", nil, "list of auditor MSP directories containing the corresponding auditor certificate")
	flags.StringSliceVarP(&genArgs.Issuers, "issuers", "s", nil, "list of issuer MSP directories containing the corresponding issuer certificate")
	flags.StringSliceVarP(&genArgs.IdemixMSPDirs, "idemix", "i", nil, "list of idemix msp dirs (at least one; earlier entries are preferred by wallets)")
	flags.StringVarP(&genArgs.ProofSystem, "proof-system", "p", DefaultProofSystem, "proof system: groth16, plonk or plonkfri (plonkfri supports only bn254)")
	flags.StringVarP(&genArgs.Curve, "curve", "c", DefaultCurve, "SNARK curve: bn254 or bls12_381")
	flags.Uint64VarP(&genArgs.MaxBits, "bits", "b", 64, "maximum bit length of a token value (1..64); changing it changes the circuits")
	flags.Uint64VarP(&genArgs.Precision, "precision", "", 0, "token quantity precision; 0 means equal to --bits; must not exceed --bits")
	flags.BoolVarP(&genArgs.Aries, "aries", "r", false, "flag to indicate that aries should be used as backend for idemix")
	flags.Uint32VarP(&genArgs.Version, "version", "v", 0, "allows the caller of tokengen to override the version number put in the public params")
	flags.StringArrayVarP(&genArgs.Extras, "extra", "x", []string{}, "extra data in key=value format, where value is the path to a file containing the data to load and store in the key")

	return c
}

// Gen generates the public parameters for the ZKAT-SNARK driver, runs the
// circuit setup, writes them to zkatsnarkv<version>_pp.json in the output
// directory, and returns the serialized public parameters.
func Gen(args *GeneratorArgs) ([]byte, error) {
	if args == nil {
		return nil, errors.New("nil generator arguments")
	}
	if args.MaxBits == 0 || args.MaxBits > 64 {
		return nil, errors.Errorf("invalid bits [%d], must be in [1, 64]", args.MaxBits)
	}
	proofSystem := args.ProofSystem
	if proofSystem == "" {
		proofSystem = DefaultProofSystem
	}
	proofSystem, err := setup.ParseProofSystem(proofSystem)
	if err != nil {
		return nil, err
	}
	curveName := args.Curve
	if curveName == "" {
		curveName = DefaultCurve
	}
	curve, err := setup.ParseCurve(curveName)
	if err != nil {
		return nil, err
	}
	if len(args.IdemixMSPDirs) == 0 {
		return nil, errors.New("at least one idemix msp dir is required")
	}

	// Load Idemix Issuer Public Keys
	idemixCurve := math3.BN254
	if args.Aries {
		idemixCurve = math3.BLS12_381_BBS_GURVY
	}
	idemixKeys := make([]*pp.IdemixIssuerPublicKey, 0, len(args.IdemixMSPDirs))
	for _, dir := range args.IdemixMSPDirs {
		_, ipkBytes, err := idemix.LoadIssuerPublicKey(dir)
		if err != nil {
			return nil, errors.Wrap(err, "failed to load issuer public key")
		}
		idemixKeys = append(idemixKeys, &pp.IdemixIssuerPublicKey{PublicKey: ipkBytes, Curve: idemixCurve})
	}

	// issuers and auditors
	auditors, err := loadIdentities(args.Auditors)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to load auditor identities")
	}
	issuers, err := loadIdentities(args.Issuers)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to load issuer identities")
	}

	// load extras
	extras, err := common.LoadExtras(args.Extras)
	if err != nil {
		return nil, errors.Wrap(err, "failed loading extras")
	}

	fmt.Println(setupNotice(proofSystem))
	p, err := setup.GeneratePublicParams(setup.GenerateOptions{
		Version:                driver.TokenDriverVersion(args.Version),
		ProofSystem:            proofSystem,
		Curve:                  curve,
		MaxBits:                int(args.MaxBits), // #nosec G115 -- checked to be in [1, 64]
		Precision:              args.Precision,
		IdemixIssuerPublicKeys: idemixKeys,
		Issuers:                issuers,
		Auditors:               auditors,
		Extras:                 extras,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed setting up public parameters")
	}

	return writePublicParams(p, args.OutputDir, false)
}

// setupNotice returns the trust caveat that applies to the setup of the passed proof system.
func setupNotice(proofSystem string) string {
	switch proofSystem {
	case setup.ProofSystemGroth16:
		return "Running groth16 circuit setup: this is a single-party trusted setup (the process knows the toxic waste), not suitable for production."
	case setup.ProofSystemPlonk:
		return "Running plonk circuit setup: the KZG SRS is generated locally with gnark's unsafekzg, not suitable for production."
	default:
		return "Running " + proofSystem + " circuit setup: transparent setup, no trusted setup required."
	}
}

// loadIdentities resolves the x509 identities in the passed MSP directories.
func loadIdentities(dirs []string) ([]driver.Identity, error) {
	ids := make([]driver.Identity, 0, len(dirs))
	for _, dir := range dirs {
		id, err := common.GetX509Identity(dir)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get identity [%s]", dir)
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// writePublicParams warns on open-policy public parameters, serializes them, and
// writes them to zkatsnarkv<version>_pp.json in outputDir. If refuseOverwrite is
// set, an existing output file is an error.
func writePublicParams(p *pp.PublicParams, outputDir string, refuseOverwrite bool) ([]byte, error) {
	// warn in case no issuers are specified
	if err := p.ValidateForDeployment(); err != nil {
		fmt.Println("WARNING: " + err.Error())
	}

	raw, err := p.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing public parameters")
	}
	fileName := fmt.Sprintf("zkatsnarkv%d_pp.json", p.SchemeVersion)
	path := filepath.Join(outputDir, fileName)
	if refuseOverwrite {
		if _, err := os.Stat(path); err == nil { //nolint:gosec // output path is chosen by the CLI user
			return nil, errors.Errorf("%s exists in the output folder. Specify another output folder with -o", fileName)
		}
	}
	if err := os.WriteFile(path, raw, 0644); err != nil { //nolint:gosec // output path is chosen by the CLI user
		return nil, errors.Wrap(err, "failed writing public parameters to file")
	}

	return raw, nil
}
