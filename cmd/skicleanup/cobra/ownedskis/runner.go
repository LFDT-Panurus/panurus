/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"slices"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	qcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Owner is a distinct (identity, type) pair that owns at least one token.
type Owner struct {
	Identity []byte
	Type     string
}

// OwnerSource yields the distinct owners of the tokens owned by this node.
type OwnerSource interface {
	// ForEachOwner calls fn once per owner. It stops at the first error.
	ForEachOwner(ctx context.Context, fn func(Owner) error) error
}

// SKIExtractor derives SKIs from an owner identity.
type SKIExtractor interface {
	GetSKIsFromIdentity(ctx context.Context, identity []byte, identityType string) ([]string, error)
}

// SQLOwnerSource reads owners from the Tokens table.
// It selects the distinct owners of every token with owner = true, spent or not,
// without joining the Ownership table: tokens stored through the standard wallet
// path have no Ownership row.
type SQLOwnerSource struct {
	DB              *sql.DB
	TokensTable     string
	CondInterpreter qcommon.CondInterpreter
}

// ForEachOwner implements OwnerSource.
func (s *SQLOwnerSource) ForEachOwner(ctx context.Context, fn func(Owner) error) error {
	query, args := q.SelectDistinct().
		FieldsByName("owner_identity", "owner_type").
		From(q.Table(s.TokensTable)).
		Where(cond.Eq("owner", true)).
		Format(s.CondInterpreter)

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return errors.Wrapf(err, "query owners from table [%s]", s.TokensTable)
	}
	defer sqlcommon.Close(rows)

	for rows.Next() {
		var o Owner
		if err := rows.Scan(&o.Identity, &o.Type); err != nil {
			return errors.Wrap(err, "scan owner")
		}
		if err := fn(o); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "iterate owners")
	}

	return nil
}

// Run returns the sorted, de-duplicated SKIs of all owners in src.
// Warnings and statistics go to errOut. If any owner fails SKI extraction,
// Run reports every failure on errOut and returns an error with no list,
// so a caller never sees a partial list.
func Run(ctx context.Context, src OwnerSource, extractor SKIExtractor, errOut io.Writer) ([]string, error) {
	skiSet := map[string]struct{}{}
	owners, failures := 0, 0

	err := src.ForEachOwner(ctx, func(o Owner) error {
		owners++
		skis, err := extractor.GetSKIsFromIdentity(ctx, o.Identity, o.Type)
		if err != nil {
			failures++
			h := sha256.Sum256(o.Identity)
			if _, werr := fmt.Fprintf(errOut, "ERROR: owner type [%s] identity sha256 [%s]: %v\n", o.Type, hex.EncodeToString(h[:]), err); werr != nil {
				return errors.Wrap(werr, "write warning")
			}

			return nil
		}
		for _, ski := range skis {
			skiSet[ski] = struct{}{}
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "list owners")
	}

	if _, err := fmt.Fprintf(errOut,
		"--- Statistics ---\n  Owners processed : %d\n  Distinct SKIs    : %d\n  Errors           : %d\n",
		owners, len(skiSet), failures); err != nil {
		return nil, errors.Wrap(err, "write statistics")
	}

	if failures > 0 {
		return nil, errors.Errorf("SKI extraction failed for %d owner(s); no list produced", failures)
	}

	skis := make([]string, 0, len(skiSet))
	for ski := range skiSet {
		skis = append(skis, ski)
	}
	slices.Sort(skis)

	return skis, nil
}
