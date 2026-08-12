/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cc

import (
	"bytes"
	"encoding/base64"
	"io"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/ccpackage"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/packager"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/packager/replacer"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// GeneratePackage generates the chaincode package for the given raw public parameters.
func GeneratePackage(raw []byte, outputDir string) error {
	t, err := template.New("node").Funcs(template.FuncMap{
		"Params": func() string { return base64.StdEncoding.EncodeToString(raw) },
	}).Parse(DefaultParams)
	if err != nil {
		return errors.Wrap(err, "failed creating params template")
	}
	paramsFile := bytes.NewBuffer(nil)
	err = t.Execute(io.MultiWriter(paramsFile), nil)
	if err != nil {
		return errors.Wrap(err, "failed writing params template")
	}

	err = packager.New().PackageChaincode(
		ccpackage.ChaincodePath,
		"golang",
		"tcc",
		filepath.Join(outputDir, "tcc.tar"),
		paramsReplacer(paramsFile.Bytes()),
	)
	if err != nil {
		return errors.Wrap(err, "failed creating chaincode package")
	}

	return nil
}

// paramsReplacer returns the replacer that swaps the public parameters file of
// the token chaincode with params while it is packaged. The packager passes the
// absolute path of each file it packages.
func paramsReplacer(params []byte) replacer.Func {
	return func(filePath string, fileName string) (string, []byte) {
		if strings.HasSuffix(filePath, ccpackage.ParamsFileSuffix) {
			return "", params
		}

		return "", nil
	}
}
