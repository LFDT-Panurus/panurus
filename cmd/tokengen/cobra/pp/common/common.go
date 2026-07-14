/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	signcerts = "signcerts"
)

// PP defines an interface shared by all public parameters.
type PP interface {
	// AddAuditor adds an auditor to the public parameters.
	AddAuditor(raw driver.Identity)
	// AddIssuer adds an issuer to the public parameters.
	AddIssuer(raw driver.Identity)
}

// GetX509Identity returns the x509 identity from the passed entry directory.
// It expects to find certificates in a 'signcerts' subdirectory within the entry directory.
func GetX509Identity(entry string) (driver.Identity, error) {
	// read certificate from entries[0]/signcerts
	signcertDir := filepath.Join(entry, signcerts)
	content, err := GetCertificatesFromDir(signcertDir)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load certificates from %s", signcertDir)
	}
	if len(content) == 0 {
		return nil, errors.Errorf("no certificates found in %s", signcertDir)
	}

	wrap, err := identity.WrapWithType(x509.IdentityType, content[0])
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to wrap x509 identity for [%s]", entry)
	}

	return wrap, nil
}

// SetupIssuersAndAuditors sets up the issuers and auditors for the given public parameters.
// It takes a list of directory paths for auditors and issuers.
func SetupIssuersAndAuditors(pp PP, Auditors, Issuers []string) error {
	// Auditors
	for _, auditor := range Auditors {
		id, err := GetX509Identity(auditor)
		if err != nil {
			return errors.WithMessagef(err, "failed to get auditor identity [%s]", auditor)
		}
		pp.AddAuditor(id)
	}
	// Issuers
	for _, issuer := range Issuers {
		id, err := GetX509Identity(issuer)
		if err != nil {
			return errors.WithMessagef(err, "failed to get issuer identity [%s]", issuer)
		}
		pp.AddIssuer(id)
	}

	return nil
}

// ReadSingleCertificateFromFile reads the passed file and checks that it contains only one
// certificate in the PEM format.
// It returns an error if the file contains more than one certificate.
func ReadSingleCertificateFromFile(file string) ([]byte, error) {
	bytes, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.Wrapf(err, "reading from file %s failed", file)
	}

	b, rest := pem.Decode(bytes)
	if b == nil {
		return nil, errors.Errorf("no pem content for file %s", file)
	}
	if len(rest) != 0 {
		return nil, errors.Errorf("extra content after pem file %s", file)
	}
	if b.Type != "CERTIFICATE" {
		return nil, errors.Errorf("pem file %s is not a certificate", file)
	}

	return bytes, nil
}

// GetCertificatesFromDir returns the PEM-encoded certificates from the given directory.
func GetCertificatesFromDir(dir string) ([][]byte, error) {
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, err
	}
	var content [][]byte
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.Wrapf(err, "could not read directory %s", dir)
	}
	errs := []string{}
	for _, f := range files {
		fullName := filepath.Join(dir, f.Name())
		f, err := os.Stat(fullName)
		if err != nil {
			errs = append(errs, fmt.Sprintf("error reading %s: %s", fullName, err.Error()))

			continue
		}
		if f.IsDir() {
			errs = append(errs, "is a directory: "+fullName)

			continue
		}
		item, err := ReadSingleCertificateFromFile(fullName)
		if err != nil {
			errs = append(errs, err.Error())

			continue
		}
		content = append(content, item)
	}
	if len(content) == 0 {
		return content, errors.New(strings.Join(errs, ", "))
	}

	return content, nil
}

// LoadExtras parses strings in the format "key=file_path" and returns a map
// where keys are the extracted keys and values are the file contents as []byte.
func LoadExtras(extraFiles []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(extraFiles))

	for _, entry := range extraFiles {
		// Split on the first colon to get key and filepath
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("invalid format %q: expected key:filepath", entry)
		}

		key := parts[0]
		filepath := parts[1]

		// Check for empty key or filepath
		if key == "" {
			return nil, errors.Errorf("empty key in entry %q", entry)
		}
		if filepath == "" {
			return nil, errors.Errorf("empty filepath for key %q", key)
		}

		// Read the file
		content, err := os.ReadFile(filepath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read file for key %q (path: %s)", key, filepath)
		}

		result[key] = content
	}

	return result, nil
}
