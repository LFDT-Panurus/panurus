/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

const (
	defaultPrefix = "fsc"
	// sharedNameProbe is the short code used to validate the part of a table name
	// that every short code shares: the prefix and the params. It is a real
	// canonical short code rather than a synthetic one, so a failure reports a name
	// the operator actually recognises, and it is never read from the overrides, so
	// the check does not depend on them.
	sharedNameProbe = "movements"
)

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
	"chk_findings":     {},
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
	Findings               string
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

	return buildTableNames(prefix, params, overrides, nc.Format)
}

// GetTableNamesWithOverridesSkipPrefix is like GetTableNamesWithOverrides but
// omits the FSC-generated prefix from every table name.
func GetTableNamesWithOverridesSkipPrefix(prefix string, overrides TableNamesConfig, params ...string) (TableNames, error) {
	nc, err := ncProvider.GetFormatter(prefix)
	if err != nil {
		return TableNames{}, err
	}

	return buildTableNames(prefix, params, overrides, nc.FormatWithoutPrefix)
}

// buildTableNames constructs a TableNames value by applying format to each
// canonical short code (after resolving any override), forwarding params.
//
// format is the error-returning formatter on purpose: an illegal character in
// the prefix, in a short-code override or in one of the params (network,
// channel, namespace) must surface as a configuration error to the caller, not
// as a panic at store-construction time.
func buildTableNames(prefix string, params []string, overrides TableNamesConfig, format func(string, ...string) (string, error)) (TableNames, error) {
	// Warn on unknown override keys before applying any overrides.
	for k := range overrides {
		if _, ok := knownShortCodes[k]; !ok {
			logger.Warnf("unknown table name override key %q — ignored", k)
		}
	}

	// The prefix and the params are shared by every short code, so a problem there
	// breaks all of the names in the same way. Validate that shared part once, with
	// a short code that is always a legal identifier fragment, so it is reported
	// once instead of repeated for every table.
	if _, err := format(sharedNameProbe, params...); err != nil {
		return TableNames{}, errors.WithMessage(err, "invalid table name prefix or parameters")
	}

	// Past that point a failure can only come from the short code itself, and an
	// override is specific to its own key, so collect them all: returning just the
	// first would make an operator fix one key, restart, and hit the next.
	var errs []error

	// name formats the effective short code for defaultCode: the override value
	// if present, otherwise the canonical default.
	name := func(defaultCode string) string {
		code := defaultCode
		if v, ok := overrides[defaultCode]; ok {
			code = v
		}
		tableName, err := format(code, params...)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to build table name for short code [%s]", code))
		}

		return tableName
	}

	tableNames := TableNames{
		Prefix:                 prefix,
		Params:                 params,
		Movements:              name("movements"),
		Transactions:           name("txs"),
		TransactionEndorseAck:  name("tx_ends"),
		Requests:               name("requests"),
		Validations:            name("req_vals"),
		Tokens:                 name("tokens"),
		Ownership:              name("tkn_own"),
		Certifications:         name("tkn_crts"),
		TokenLocks:             name("tkn_locks"),
		PublicParams:           name("public_params"),
		Wallets:                name("wallets"),
		IdentityConfigurations: name("id_cfgs"),
		IdentityInfo:           name("id_info"),
		Signers:                name("id_signers"),
		KeyStore:               name("key_store"),
		EIDLeases:              name("eid_leases"),
		TokenSKICleanups:       name("tkn_ski_cleanups"),
		Findings:               name("chk_findings"),
	}
	if err := errors.Join(errs...); err != nil {
		return TableNames{}, err
	}

	return tableNames, nil
}
