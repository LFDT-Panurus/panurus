/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"regexp"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

var (
	// validName accepts the SQL identifiers this package is allowed to generate:
	// letters, digits and underscores, never starting with a digit (unquoted
	// identifiers in both SQLite and PostgreSQL must start with a letter or an
	// underscore). All regexps here are thread safe.
	validName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	// validEscapedParams accepts the result of escaping the table name
	// parameters. Digits are allowed in any position, including the first: the
	// composed table name is checked against validName afterwards, which is what
	// decides whether a leading digit is acceptable there.
	validEscapedParams = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	// validPrefix accepts the user-configured table prefix. It is deliberately
	// stricter than validName: a prefix is a configuration value, and letters
	// and underscores have always been its documented character set.
	validPrefix = regexp.MustCompile(`^[a-zA-Z_]+$`)
	replacers   = []*replacer{
		newReplacer("_", "__"),
		newReplacer("-", "_d"),
		newReplacer("\\.", "_f"),
	}
)

type TableNameCreator struct {
	formatterProvider lazy.Provider[string, *tableNameFormatter]
}

func NewTableNameCreator(defaultPrefix string) *TableNameCreator {
	return &TableNameCreator{formatterProvider: lazy.NewProvider(func(prefix string) (*tableNameFormatter, error) {
		if len(prefix) > 100 {
			return nil, errors.New("table prefix must be shorter than 100 characters")
		}
		if len(prefix) == 0 {
			prefix = defaultPrefix
		}
		if len(prefix) == 0 {
			return &tableNameFormatter{r: validName}, nil
		}

		if !validPrefix.MatchString(prefix) {
			return nil, errors.New("illegal character in table prefix, only letters and underscores allowed")
		}

		return &tableNameFormatter{
			prefix: strings.ToLower(prefix) + "_",
			r:      validName,
		}, nil
	})}
}

func (c *TableNameCreator) GetFormatter(prefix string) (*tableNameFormatter, error) {
	return c.formatterProvider.Get(prefix)
}

func (c *TableNameCreator) MustGetTableName(tablePrefix, name string, params ...string) string {
	return utils.MustGet(c.CreateTableName(tablePrefix, name, params...))
}

func (c *TableNameCreator) CreateTableName(tablePrefix, name string, params ...string) (string, error) {
	nc, err := c.formatterProvider.Get(tablePrefix)
	if err != nil {
		return "", err
	}

	return nc.Format(name, params...)
}

type replacer struct {
	regex *regexp.Regexp
	repl  string
}

type tableNameFormatter struct {
	prefix string
	r      *regexp.Regexp
}

func (c *tableNameFormatter) MustFormat(name string, params ...string) string {
	return utils.MustGet(c.Format(name, params...))
}

func (c *tableNameFormatter) Format(name string, params ...string) (string, error) {
	return c.format(c.prefix, name, params...)
}

// FormatWithoutPrefix returns the table name without applying the prefix.
func (c *tableNameFormatter) FormatWithoutPrefix(name string, params ...string) (string, error) {
	return c.format("", name, params...)
}

// format composes prefix, the escaped params and name, and validates the result.
// It validates the identifier it is about to emit, not just the name fragment,
// so a param that starts with a digit is fine as long as a prefix precedes it —
// and rejected when there is none. It returns an error, never panics, for any
// input it cannot turn into a legal SQL identifier.
func (c *tableNameFormatter) format(prefix, name string, params ...string) (string, error) {
	if len(params) > 0 {
		escaped, err := escapeForTableName(params...)
		if err != nil {
			return "", err
		}
		name = escaped + "_" + name
	}
	tableName := prefix + name
	if !c.r.MatchString(tableName) {
		return "", errors.Errorf("invalid table name [%s]: only letters, digits and underscores are allowed, and it cannot start with a digit", tableName)
	}

	return tableName, nil
}

// MustFormatWithoutPrefix returns the table name without applying the prefix, panicking on error.
func (c *tableNameFormatter) MustFormatWithoutPrefix(name string, params ...string) string {
	return utils.MustGet(c.FormatWithoutPrefix(name, params...))
}

func newReplacer(escaped, repl string) *replacer {
	return &replacer{
		regex: regexp.MustCompile(escaped),
		repl:  repl,
	}
}

func (r *replacer) Escape(s string) string {
	return r.regex.ReplaceAllString(s, r.repl)
}

// escapeForTableName joins params and escapes the characters that are legal in a
// network/channel/namespace name but not in a SQL identifier. It returns an
// error, rather than panicking, when a parameter contains a character it cannot
// escape, so that a bad configuration value surfaces as a store-construction
// error instead of crashing the node.
func escapeForTableName(params ...string) (string, error) {
	name := strings.Join(params, "_")
	for _, r := range replacers {
		name = r.Escape(name)
	}
	if len(name) > 0 && !validEscapedParams.MatchString(name) {
		return "", errors.Errorf("unsupported chars found in table name parameters [%s]: only letters, digits, underscores, dashes and dots are allowed", name)
	}

	return name, nil
}
