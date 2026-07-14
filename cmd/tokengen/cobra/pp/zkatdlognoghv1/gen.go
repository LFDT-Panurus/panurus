/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatdlognoghv1

import (
	"fmt"
	"os"
	"path/filepath"

	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/cc"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/idemix"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	setupv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

// GeneratorArgs defines the arguments for the public parameters generator.
type GeneratorArgs struct {
	// IdemixMSPDir is the directory containing the Idemix MSP config (Issuer Key Pair)
	IdemixMSPDir string
	// OutputDir is the directory to output the generated files
	OutputDir string
	// GenerateCCPackage indicates whether to generate the chaincode package
	GenerateCCPackage bool
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate
	Auditors []string
	// BitLength is a dlog driver related parameter
	BitLength uint64
	// Aries is a flag to indicate that aries should be used as backend for idemix
	Aries bool
	// Version allows the caller of tokengen to override the version number put in the public params
	Version uint32
}

var (
	// IdemixMSPDir is the directory containing the Idemix MSP config (Issuer Key Pair)
	IdemixMSPDir string
	// OutputDir is the directory to output the generated files
	OutputDir string
	// GenerateCCPackage indicates whether to generate the chaincode package
	GenerateCCPackage bool
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate
	Auditors []string
	// BitLength is a dlog driver related parameter.
	// It is used to define the maximum quantity a token can contain
	BitLength uint64
	// Aries is a flag to indicate that aries should be used as backend for idemix
	Aries bool
	// Version allows the caller of tokengen to override the version number put in the public params
	Version uint32
	// Extras allows the caller to add extra parameters to the public parameters
	Extras []string
)

// Cmd returns the Cobra Command for ZKAT DLog public parameters generation.
func Cmd() *cobra.Command {
	// Set the flags on the node start command.
	flags := cobraCommand.Flags()
	flags.StringVarP(&OutputDir, "output", "o", ".", "output folder")
	flags.BoolVarP(&GenerateCCPackage, "cc", "", false, "generate chaincode package")
	flags.StringSliceVarP(&Auditors, "auditors", "a", nil, "list of auditor MSP directories containing the corresponding auditor certificate")
	flags.StringSliceVarP(&Issuers, "issuers", "s", nil, "list of issuer MSP directories containing the corresponding issuer certificate")
	flags.StringVarP(&IdemixMSPDir, "idemix", "i", "", "idemix msp dir")
	flags.Uint64VarP(&BitLength, "bits", "b", 64, "bits is used to define the maximum quantity a token can contain")
	flags.BoolVarP(&Aries, "aries", "r", false, "flag to indicate that aries should be used as backend for idemix")
	flags.Uint32VarP(&Version, "version", "v", 0, "allows the caller of tokengen to override the version number put in the public params")
	flags.StringArrayVarP(&Extras, "extra", "x", []string{}, "extra data in key=value format, where value is the path to a file containing the data to load and store in the key")

	return cobraCommand
}

var cobraCommand = &cobra.Command{
	Use:   zkatdlognoghv1.DriverIdentifier,
	Short: "Gen ZKAT DLog public parameters.",
	Long:  `Generates ZKAT DLog public parameters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return errors.New("trailing args detected")
		}
		// Parsing of the command line is done so silence cmd usage
		cmd.SilenceUsage = true
		raw, err := Gen(&GeneratorArgs{
			IdemixMSPDir:      IdemixMSPDir,
			OutputDir:         OutputDir,
			GenerateCCPackage: GenerateCCPackage,
			Issuers:           Issuers,
			Auditors:          Auditors,
			BitLength:         BitLength,
			Aries:             Aries,
			Version:           Version,
		})
		if err != nil {
			fmt.Printf("failed to generate public parameters [%s]\n", err)

			return errors.Wrap(err, "failed to generate public parameters")
		}
		// generate the chaincode package
		if GenerateCCPackage {
			fmt.Println("Generate chaincode package...")
			if err := cc.GeneratePackage(raw, OutputDir); err != nil {
				return err
			}
		}

		return nil
	},
}

// Gen generates the public parameters for the ZKATDLog driver.
func Gen(args *GeneratorArgs) ([]byte, error) {
	// Load Idemix Issuer Public Key
	_, ipkBytes, err := idemix.LoadIssuerPublicKey(args.IdemixMSPDir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load issuer public key")
	}

	// Setup
	// TODO: update the curve here
	curveID := math3.BN254
	if args.Aries {
		curveID = math3.BLS12_381_BBS_GURVY
	}
	ver := setupv1.ProtocolV1
	if args.Version != 0 {
		ver = driver.TokenDriverVersion(args.Version)
	}
	var bitLength uint64
	if args.BitLength != 0 {
		bitLength = args.BitLength
	}
	pp, err := setupv1.WithVersion(bitLength, ipkBytes, curveID, ver)
	if err != nil {
		return nil, errors.Wrap(err, "failed setting up public parameters")
	}

	// issuers and auditors
	if err := common.SetupIssuersAndAuditors(pp, args.Auditors, args.Issuers); err != nil {
		return nil, errors.Wrap(err, "failed to setup issuer and auditors")
	}

	// load extras
	pp.ExtraData, err = common.LoadExtras(Extras)
	if err != nil {
		return nil, errors.Wrap(err, "failed loading extras")
	}

	// validate
	if err := pp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed to validate public parameters")
	}

	// warn in case no issuers are specified
	if len(pp.Issuers()) == 0 {
		fmt.Println("No issuers specified. The public parameters allow anyone to create tokens.")
	}

	// Store Public Params
	raw, err := pp.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing public parameters")
	}
	path := filepath.Join(
		args.OutputDir,
		fmt.Sprintf("zkatdlognoghv%d_pp.json", ver),
	)
	if err := os.WriteFile(path, raw, 0755); err != nil {
		return nil, errors.Wrap(err, "failed writing public parameters to file")
	}

	return raw, nil
}
