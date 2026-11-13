/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatdlognoghv1

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	ppencoding "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
)

// InputFile is the file that contains the public parameters
var InputFile string

// UpdateArgs defines the arguments for updating public parameters.
type UpdateArgs struct {
	// InputFile is the file that contains the public parameters
	InputFile string
	// OutputDir is the directory to output the generated files
	OutputDir string
	// Issuers is the list of issuer MSP directories containing the corresponding issuer certificate
	Issuers []string
	// Auditors is the list of auditor MSP directories containing the corresponding auditor certificate
	Auditors []string
	// Version allows the caller of tokengen to override the version number put in the public params.
	// If zero, the version stored in the input file is kept.
	Version uint32
	// Extras is the list of extra data entries in key=value format, where value is the path to a file
	// containing the data to load and store in the key
	Extras []string
}

// UpdateCmd returns the Cobra Command for updating public parameters.
func UpdateCmd() *cobra.Command {
	// Set the flags on the node start command.
	flags := cmd.Flags()
	flags.StringVarP(&InputFile, "input", "i", "", "path of the public param file")
	flags.StringVarP(&OutputDir, "output", "o", ".", "output folder")
	flags.StringSliceVarP(&Auditors, "auditors", "a", nil, "list of auditor MSP directories containing the corresponding auditor certificate")
	flags.StringSliceVarP(&Issuers, "issuers", "s", nil, "list of issuer MSP directories containing the corresponding issuer certificate")
	flags.Uint32VarP(&Version, "version", "v", 0, "allows the caller of tokengen to override the version number put in the public params")
	flags.StringArrayVarP(&Extras, "extra", "x", []string{}, "extra data in key=value format, where is the path to a file containing the data to load and store in the key")

	return cmd
}

var cmd = &cobra.Command{
	Use:   zkatdlognoghv1.DriverIdentifier,
	Short: "Update certs in the public parameters file.",
	Long:  "Update certs in the public parameters file without changing the parameters themselves.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return errors.New("trailing args detected")
		}
		// Parsing of the command line is done so silence cmd usage
		cmd.SilenceUsage = true
		err := Update(&UpdateArgs{
			InputFile: InputFile,
			OutputDir: OutputDir,
			Issuers:   Issuers,
			Auditors:  Auditors,
			Version:   Version,
			Extras:    Extras,
		})
		if err != nil {
			return errors.Wrap(err, "failed to generate public parameters")
		}

		return nil
	},
}

// Update updates the public parameters with new certificates.
func Update(args *UpdateArgs) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.Errorf("failed updating public parameters: %v", e)
		}
	}()
	oldraw, err := os.ReadFile(args.InputFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read input file at [%s]", args.InputFile)
	}

	inputVersion, err := driverVersion(oldraw)
	if err != nil {
		return errors.Wrapf(err, "failed to read driver version from [%s]", args.InputFile)
	}
	pp, err := v1.NewPublicParamsFromBytes(oldraw, v1.DLogNoGHDriverName, inputVersion)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal pp from [%s]", args.InputFile)
	}
	if err := pp.Validate(); err != nil {
		return errors.Wrapf(err, "failed to validate public parameters")
	}

	// Clear auditor and issuers if provided, and add them again.
	// If not provided, do not change them.
	if len(args.Auditors) > 0 {
		if err := pp.SetAuditors(nil); err != nil {
			return errors.Wrapf(err, "failed to clear auditors")
		}
	}
	if len(args.Issuers) > 0 {
		pp.SetIssuers(nil)
	}
	if err := common.SetupIssuersAndAuditors(pp, args.Auditors, args.Issuers); err != nil {
		return err
	}

	// update version, if needed; otherwise keep the version of the input file
	ver := inputVersion
	if args.Version != 0 {
		ver = driver.TokenDriverVersion(args.Version)
	}
	pp.DriverVersion = ver

	// update extras, if needed
	extraData, err := common.LoadExtras(args.Extras)
	if err != nil {
		return errors.Wrap(err, "failed loading extras")
	}
	maps.Copy(pp.ExtraData, extraData)

	// Store Public Params
	raw, err := pp.Serialize()
	if err != nil {
		return errors.Wrap(err, "failed serializing public parameters")
	}
	fileName := fmt.Sprintf("zkatdlognoghv%d_pp.json", ver)
	path := filepath.Join(
		args.OutputDir,
		fileName,
	)
	if _, err := os.Stat(path); err == nil {
		return errors.Errorf("%s exists in current directory. Specify another output folder with -o", fileName)
	}
	if err := os.WriteFile(path, raw, 0755); err != nil { //nolint:gosec
		return errors.Wrap(err, "failed writing public parameters to file")
	}

	return nil
}

// driverVersion returns the driver version encoded in the identifier of the serialized public parameters.
// The identifier has the form <driver name>.v<driver version>, and the driver name must be the
// zkatdlog nogh one.
func driverVersion(raw []byte) (driver.TokenDriverVersion, error) {
	container, err := ppencoding.Unmarshal(raw)
	if err != nil {
		return 0, errors.Wrap(err, "failed to deserialize public parameters")
	}
	prefix := string(v1.DLogNoGHDriverName) + ".v"
	if !strings.HasPrefix(container.Identifier, prefix) {
		return 0, errors.Errorf("invalid identifier, expecting prefix [%s], got [%s]", prefix, container.Identifier)
	}
	ver, err := strconv.ParseUint(strings.TrimPrefix(container.Identifier, prefix), 10, 32)
	if err != nil {
		return 0, errors.Wrapf(err, "invalid driver version in identifier [%s]", container.Identifier)
	}

	return driver.TokenDriverVersion(ver), nil //nolint:gosec // ParseUint with bitSize 32 bounds ver to uint32
}
