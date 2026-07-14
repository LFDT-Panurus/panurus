/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

type (
	// PPHash is used to model the hash of the raw public parameters.
	// This should avoid confusion between the bytes of the public params themselves and its hash.
	PPHash []byte
	// TokenDriverName is the name of a token driver
	TokenDriverName string
	// TokenDriverVersion is the version of a token driver
	TokenDriverVersion uint32
)

type PPReader interface {
	// PublicParametersFromBytes unmarshals the bytes to a PublicParameters instance.
	PublicParametersFromBytes(params []byte) (PublicParameters, error)
}

// PPMFactory contains the static logic of the driver
//
//go:generate counterfeiter -o mock/ppm_factory.go -fake-name PPMFactory . PPMFactory
type PPMFactory interface {
	PPReader
	// NewPublicParametersManager returns a new PublicParametersManager instance from the passed public parameters
	NewPublicParametersManager(pp PublicParameters) (PublicParamsManager, error)
}

// PublicParamsFetcher models a public parameters fetcher.
//
//go:generate counterfeiter -o mock/pp_fetcher.go -fake-name PublicParamsFetcher . PublicParamsFetcher
type PublicParamsFetcher interface {
	// Fetch fetches the public parameters from a repository.
	Fetch() ([]byte, error)
}

type Extras = map[string][]byte

// PublicParameters defines the common interface for a driver's public parameters.
// These parameters are shared among all participants in the token network and
// define the rules and characteristics of the token system.
//
//go:generate counterfeiter -o mock/pp.go -fake-name PublicParameters . PublicParameters
type PublicParameters interface {
	// TokenDriverName returns the unique name of the token driver.
	TokenDriverName() TokenDriverName
	// TokenDriverVersion returns the version of the token driver for which these parameters are valid.
	TokenDriverVersion() TokenDriverVersion
	// TokenDataHiding indicates whether token values and types are hidden (obfuscated).
	TokenDataHiding() bool
	// GraphHiding indicates whether the transaction graph (linkage between tokens) is hidden.
	GraphHiding() bool
	// MaxTokenValue returns the maximum value any single token can have.
	MaxTokenValue() uint64
	// CertificationDriver returns the identifier of the certification driver, if any.
	CertificationDriver() string
	// Auditors returns the list of identities authorized to audit transactions.
	Auditors() []Identity
	// Issuers returns the list of identities authorized to issue tokens.
	Issuers() []Identity
	// Precision returns the numeric precision used for token values.
	Precision() uint64
	// String provides a human-readable representation of the public parameters.
	String() string
	// Serialize converts the public parameters into their byte representation.
	Serialize() ([]byte, error)
	// Validate checks if the public parameters are internally consistent and valid.
	Validate() error
	// Extras provides access to additional, driver-specific parameters.
	Extras() Extras
}

// PublicParamsManager provides methods for managing and accessing the driver's public parameters.
// It also facilitates the generation of cryptographic materials like certifier key pairs.
//
//go:generate counterfeiter -o mock/ppm.go -fake-name PublicParamsManager . PublicParamsManager
type PublicParamsManager interface {
	// PublicParameters returns the current set of public parameters.
	PublicParameters() PublicParameters
	// NewCertifierKeyPair generates a new public-private key pair for a certifier, if supported by the driver.
	NewCertifierKeyPair() ([]byte, []byte, error)
	// PublicParamsHash returns a unique hash of the serialized public parameters.
	PublicParamsHash() PPHash
}
