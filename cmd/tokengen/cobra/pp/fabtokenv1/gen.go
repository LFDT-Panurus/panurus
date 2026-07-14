/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/cc"
	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	setupv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

var (
	// Driver is the Panurus token driver to use.
	Driver string
	// OutputDir is the directory to output the generated files.
	OutputDir string
	// GenerateCCPackage indicates whether to generate the chaincode package.
	GenerateCCPackage bool
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate.
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate.
	Auditors []string
	// Version allows the caller of tokengen to override the version number put in the public parameters.
	Version uint32
	// Extras allows the caller to add extra parameters to the public parameters.
	Extras []string
)

// Cmd returns the Cobra Command for FabToken public parameters generation.
func Cmd() *cobra.Command {
	// Set the flags on the node start command.
	flags := cobraCommand.Flags()
	flags.StringVarP(&OutputDir, "output", "o", ".", "output folder")
	flags.BoolVarP(&GenerateCCPackage, "cc", "", false, "generate chaincode package")
	flags.StringSliceVarP(&Auditors, "auditors", "a", nil, "list of auditor MSP directories containing the corresponding auditor certificate")
	flags.StringSliceVarP(&Issuers, "issuers", "s", nil, "list of issuer MSP directories containing the corresponding issuer certificate")
	flags.Uint32VarP(&Version, "version", "v", 0, "allows the caller of tokengen to override the version number put in the public params")
	flags.StringArrayVarP(&Extras, "extra", "x", []string{}, "extra data in key=value format, where value is the path to a file containing the data to load and store in the key")

	return cobraCommand
}

var cobraCommand = &cobra.Command{
	Use:   fabtokenv1.DriverIdentifier,
	Short: "Gen FabToken public parameters.",
	Long:  `Generates FabToken public parameters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return errors.New("trailing args detected")
		}
		// Parsing of the command line is done so silence cmd usage
		cmd.SilenceUsage = true
		raw, err := Gen(&GeneratorArgs{
			OutputDir:         OutputDir,
			GenerateCCPackage: GenerateCCPackage,
			Issuers:           Issuers,
			Auditors:          Auditors,
		})
		if err != nil {
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

// GeneratorArgs defines the arguments for the public parameters generator.
type GeneratorArgs struct {
	// OutputDir is the directory to output the generated files.
	OutputDir string
	// GenerateCCPackage indicates whether to generate the chaincode package.
	GenerateCCPackage bool
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate.
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate.
	Auditors []string
}

// Gen generates the public parameters for the FabToken driver.
func Gen(args *GeneratorArgs) ([]byte, error) {
	// Setup
	ver := setupv1.ProtocolV1
	if Version != 0 {
		ver = driver.TokenDriverVersion(Version)
	}
	pp, err := setupv1.WithVersion(setupv1.DefaultPrecision, ver)
	if err != nil {
		return nil, errors.Wrap(err, "failed setting up public parameters")
	}
	if err := common.SetupIssuersAndAuditors(pp, args.Auditors, args.Issuers); err != nil {
		return nil, err
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
		fmt.Sprintf("fabtoken%d_pp.json", ver),
	)
	if err := os.WriteFile(path, raw, 0755); err != nil {
		return nil, errors.Wrap(err, "failed writing public parameters to file")
	}

	return raw, nil
}
