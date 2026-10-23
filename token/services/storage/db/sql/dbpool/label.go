/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbpool

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// keywordPairPattern matches one key=value pair of a PostgreSQL keyword/value
// connection string, e.g. "host=db-1 port=5432". Values may be quoted with
// single quotes to contain spaces; neither form is captured here beyond what
// PoolLabel needs (host, port, dbname) - credentials are never read out.
var keywordPairPattern = regexp.MustCompile(`(\w+)=(?:'([^']*)'|(\S+))`)

// PoolLabel derives the "pool" label value from a PostgreSQL data source: the
// host, port and database name, with no credential of any kind - unlike a
// redacted DSN, which would still carry a password if the connection string
// put it in a field redactDataSource (postgres/notifier.go) does not expect.
// The identity that matters for the pool label is "which server and
// database", not "how was it addressed", so the normalized "host:port/db"
// form is used for both of PostgreSQL's two accepted DSN syntaxes, and a
// connection string that parses as neither falls back to a short, stable hash
// so a label is never raw and unredacted even in that path.
func PoolLabel(dataSource string) string {
	if host, port, dbname, ok := parseURLDataSource(dataSource); ok {
		return formatLabel(host, port, dbname)
	}
	if host, port, dbname, ok := parseKeywordDataSource(dataSource); ok {
		return formatLabel(host, port, dbname)
	}

	return hashLabel(dataSource)
}

func formatLabel(host, port, dbname string) string {
	if port == "" {
		port = "5432"
	}
	dbname = strings.TrimPrefix(dbname, "/")
	if strings.Contains(host, ":") {
		// An IPv6 literal (url.Hostname() strips the brackets a URL DSN
		// wrote around it): without them, "::1:5432/db" reads as three
		// ambiguous colons instead of one host and one port.
		host = "[" + host + "]"
	}

	return host + ":" + port + "/" + dbname
}

// parseURLDataSource handles the "postgres://user:pass@host:port/dbname?..."
// form.
func parseURLDataSource(dataSource string) (host, port, dbname string, ok bool) {
	if !strings.HasPrefix(dataSource, "postgres://") && !strings.HasPrefix(dataSource, "postgresql://") {
		return "", "", "", false
	}
	u, err := url.Parse(dataSource)
	if err != nil {
		return "", "", "", false
	}

	return u.Hostname(), u.Port(), u.Path, true
}

// parseKeywordDataSource handles the "host=... port=... dbname=... password=..."
// form. Credentials (password, user) are deliberately never read.
func parseKeywordDataSource(dataSource string) (host, port, dbname string, ok bool) {
	matches := keywordPairPattern.FindAllStringSubmatch(dataSource, -1)
	if len(matches) == 0 {
		return "", "", "", false
	}

	for _, m := range matches {
		key := m[1]
		value := m[2]
		if value == "" {
			value = m[3]
		}
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		case "dbname":
			dbname = value
		}
	}
	if host == "" && dbname == "" {
		return "", "", "", false
	}

	return host, port, dbname, true
}

// hashLabel is the fallback for a data source PoolLabel cannot parse: a
// short, stable, non-reversible identifier so two calls on the same
// unparsed data source still produce one series rather than one per call.
func hashLabel(dataSource string) string {
	sum := sha256.Sum256([]byte(dataSource))

	return "unknown:" + hex.EncodeToString(sum[:])[:16]
}
