/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

const defaultPrefix = "fsc"

var (
	logger     = logging.MustGetLogger()
	ncProvider = NewTableNameCreator(defaultPrefix)
)

// knownShortCodes is the set of valid override keys, used to warn on unknown entries.
var knownShortCodes = map[string]struct{}{
	"movements":        {},
	"txs":              {},
	"tx_ends":          {},
	"requests":         {},
	"req_vals":         {},
	"tokens":           {},
	"tkn_own":          {},
	"tkn_crts":         {},
	"tkn_locks":        {},
	"public_params":    {},
	"wallets":          {},
	"id_cfgs":          {},
	"id_info":          {},
	"id_signers":       {},
	"key_store":        {},
	"eid_leases":       {},
	"tkn_ski_cleanups": {},
}

type TableNames struct {
	Prefix                 string
	Params                 []string
	Movements              string
	Transactions           string
	Requests               string
	Validations            string
	TransactionEndorseAck  string
	Certifications         string
	Tokens                 string
	Ownership              string
	PublicParams           string
	Wallets                string
	IdentityConfigurations string
	IdentityInfo           string
	Signers                string
	TokenLocks             string
	KeyStore               string
	EIDLeases              string
	TokenSKICleanups       string
}

type PersistenceConstructor[V common.DBObject] func(*common.RWDB, TableNames) (V, error)

// GetTableNames returns the SQL table names for the given prefix and params using
// the default short codes. It is equivalent to calling GetTableNamesWithOverrides
// with a nil overrides map.
func GetTableNames(prefix string, params ...string) (TableNames, error) {
	return GetTableNamesWithOverrides(prefix, nil, params...)
}

// GetTableNamesWithConfig returns the SQL table names using all options from cfg.
func GetTableNamesWithConfig(prefix string, cfg StorageConfig, params ...string) (TableNames, error) {
	if cfg.SkipPrefix {
		return GetTableNamesWithOverridesSkipPrefix(prefix, cfg.TableNames, params...)
	}

	return GetTableNamesWithOverrides(prefix, cfg.TableNames, params...)
}

// GetTableNamesWithOverrides returns the SQL table names for the given prefix and
// params, applying any short-code substitutions from overrides before the FSC
// formatter wraps them with the prefix and params.
//
// The overrides map key is the canonical short code (e.g. "id_signers") and the
// value is the replacement short code (e.g. "identity_signers"). Unknown keys
// produce a warning and are ignored; all other fields keep their default values.
func GetTableNamesWithOverrides(prefix string, overrides TableNamesConfig, params ...string) (TableNames, error) {
	nc, err := ncProvider.GetFormatter(prefix)
	if err != nil {
		return TableNames{}, err
	}

	return buildTableNames(prefix, params, overrides, nc.MustFormat)
}

// GetTableNamesWithOverridesSkipPrefix is like GetTableNamesWithOverrides but
// omits the FSC-generated prefix from every table name.
func GetTableNamesWithOverridesSkipPrefix(prefix string, overrides TableNamesConfig, params ...string) (TableNames, error) {
	nc, err := ncProvider.GetFormatter(prefix)
	if err != nil {
		return TableNames{}, err
	}

	return buildTableNames(prefix, params, overrides, nc.MustFormatWithoutPrefix)
}

// buildTableNames constructs a TableNames value by applying format to each
// canonical short code (after resolving any override), forwarding params.
func buildTableNames(prefix string, params []string, overrides TableNamesConfig, format func(string, ...string) string) (TableNames, error) {
	// Warn on unknown override keys before applying any overrides.
	for k := range overrides {
		if _, ok := knownShortCodes[k]; !ok {
			logger.Warnf("unknown table name override key %q — ignored", k)
		}
	}

	// resolve returns the effective short code: the override value if present,
	// otherwise the canonical default.
	resolve := func(defaultCode string) string {
		if v, ok := overrides[defaultCode]; ok {
			return v
		}

		return defaultCode
	}

	return TableNames{
		Prefix:                 prefix,
		Params:                 params,
		Movements:              format(resolve("movements"), params...),
		Transactions:           format(resolve("txs"), params...),
		TransactionEndorseAck:  format(resolve("tx_ends"), params...),
		Requests:               format(resolve("requests"), params...),
		Validations:            format(resolve("req_vals"), params...),
		Tokens:                 format(resolve("tokens"), params...),
		Ownership:              format(resolve("tkn_own"), params...),
		Certifications:         format(resolve("tkn_crts"), params...),
		TokenLocks:             format(resolve("tkn_locks"), params...),
		PublicParams:           format(resolve("public_params"), params...),
		Wallets:                format(resolve("wallets"), params...),
		IdentityConfigurations: format(resolve("id_cfgs"), params...),
		IdentityInfo:           format(resolve("id_info"), params...),
		Signers:                format(resolve("id_signers"), params...),
		KeyStore:               format(resolve("key_store"), params...),
		EIDLeases:              format(resolve("eid_leases"), params...),
		TokenSKICleanups:       format(resolve("tkn_ski_cleanups"), params...),
	}, nil
}
