/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"bytes"
	"maps"
	"os"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	pp3 "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

// UpdateArgs defines the arguments for updating ZKAT-SNARK public parameters.
type UpdateArgs struct {
	// InputFile is the file that contains the public parameters
	InputFile string
	// OutputDir is the directory to output the generated files
	OutputDir string
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate
	Auditors []string
	// Append, when set, adds the passed issuers and auditors to the existing ones
	// (skipping identities already present) instead of replacing them.
	Append bool
	// Version allows the caller of tokengen to override the version number put in the public params.
	// Zero keeps the version of the input public parameters.
	Version uint32
	// Extras lists extra data entries in key=path format, merged into the existing extras
	Extras []string
}

var updateArgs UpdateArgs

// UpdateCmd returns the Cobra Command for updating ZKAT-SNARK public parameters.
func UpdateCmd() *cobra.Command {
	updateArgs = UpdateArgs{}
	c := &cobra.Command{
		Use:   DriverIdentifier,
		Short: "Update certs in the ZKAT-SNARK public parameters file.",
		Long:  "Update issuer and auditor certs in the ZKAT-SNARK public parameters file without re-running the circuit setup.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errors.New("trailing args detected")
			}
			// Parsing of the command line is done so silence cmd usage
			cmd.SilenceUsage = true
			if err := Update(&updateArgs); err != nil {
				return errors.Wrap(err, "failed to update public parameters")
			}

			return nil
		},
	}
	flags := c.Flags()
	flags.StringVarP(&updateArgs.InputFile, "input", "i", "", "path of the public param file")
	flags.StringVarP(&updateArgs.OutputDir, "output", "o", ".", "output folder")
	flags.StringSliceVarP(&updateArgs.Auditors, "auditors", "a", nil, "list of auditor MSP directories containing the corresponding auditor certificate")
	flags.StringSliceVarP(&updateArgs.Issuers, "issuers", "s", nil, "list of issuer MSP directories containing the corresponding issuer certificate")
	flags.BoolVarP(&updateArgs.Append, "append", "", false, "append issuers and auditors to the existing ones instead of replacing them")
	flags.Uint32VarP(&updateArgs.Version, "version", "v", 0, "allows the caller of tokengen to override the version number put in the public params (0 keeps the input version)")
	flags.StringArrayVarP(&updateArgs.Extras, "extra", "x", []string{}, "extra data in key=value format, where value is the path to a file containing the data to load and store in the key")

	return c
}

// Update loads the ZKAT-SNARK public parameters in args.InputFile, replaces
// (or, with args.Append, extends) its issuers and auditors, merges extras,
// optionally overrides the version, and writes the result to
// zkatsnarkv<version>_pp.json in args.OutputDir. The circuit keys and every
// circuit-shaping parameter are preserved unchanged; the setup is not re-run.
func Update(args *UpdateArgs) error {
	if args == nil {
		return errors.New("nil update arguments")
	}
	raw, err := os.ReadFile(args.InputFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read input file at [%s]", args.InputFile)
	}

	// make sure the input carries zkatsnark public parameters
	container, err := pp3.Unmarshal(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal pp from [%s]", args.InputFile)
	}
	if !strings.HasPrefix(container.Identifier, DriverName+".v") {
		return errors.Errorf("invalid public parameters in [%s]: expected driver [%s.v*], got [%s]", args.InputFile, DriverName, container.Identifier)
	}
	p, err := pp.DeserializePublicParams(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal pp from [%s]", args.InputFile)
	}
	if len(p.PKSpend) == 0 || len(p.PKOutput) == 0 || len(p.VKSpend) == 0 || len(p.VKOutput) == 0 {
		return errors.Errorf("invalid public parameters in [%s]: circuit proving/verification keys are missing, generate them with tokengen gen %s", args.InputFile, DriverIdentifier)
	}

	// issuers and auditors
	auditors, err := loadIdentities(args.Auditors)
	if err != nil {
		return errors.WithMessage(err, "failed to load auditor identities")
	}
	issuers, err := loadIdentities(args.Issuers)
	if err != nil {
		return errors.WithMessage(err, "failed to load issuer identities")
	}
	// Replace the auditors and issuers if provided, or append them in append mode.
	// If not provided, do not change them.
	if args.Append {
		auditors = appendUnique(p.AuditorIdentities, auditors)
		issuers = appendUnique(p.IssuerIdentities, issuers)
	}
	if len(auditors) > 0 {
		if err := p.SetAuditors(auditors); err != nil {
			return errors.Wrap(err, "failed to set auditors")
		}
	}
	if len(issuers) > 0 {
		p.SetIssuers(issuers)
	}

	// update version, if needed
	if args.Version != 0 {
		p.SchemeVersion = driver.TokenDriverVersion(args.Version)
	}

	// update extras, if needed
	extras, err := common.LoadExtras(args.Extras)
	if err != nil {
		return errors.Wrap(err, "failed loading extras")
	}
	if p.ExtraParams == nil {
		p.ExtraParams = driver.Extras{}
	}
	maps.Copy(p.ExtraParams, extras)

	if err := p.Validate(); err != nil {
		return errors.Wrap(err, "failed to validate public parameters")
	}

	if _, err := writePublicParams(p, args.OutputDir, true); err != nil {
		return err
	}

	return nil
}

// appendUnique returns existing extended with the identities in toAdd that are not already present.
func appendUnique(existing, toAdd []driver.Identity) []driver.Identity {
	out := append([]driver.Identity{}, existing...)
	for _, id := range toAdd {
		found := false
		for _, e := range out {
			if bytes.Equal(e, id) {
				found = true

				break
			}
		}
		if !found {
			out = append(out, id)
		}
	}

	return out
}
