/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"fmt"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	SelectStatement         = `SELECT`
	SelectDistinctStatement = `SELECT DISTINCT`
	InsertStatement         = `INSERT INTO`
	UpdateStatement         = `UPDATE`
	DeleteStatement         = `DELETE FROM`
)

type Select struct {
	stmt    string
	columns []string
	from    []string
	where   string
	orderBy string
}

func NewSelect(columns ...string) *Select {
	return &Select{
		stmt:    SelectStatement,
		columns: columns,
	}
}

func NewSelectDistinct(columns ...string) *Select {
	return &Select{
		stmt:    SelectDistinctStatement,
		columns: columns,
	}
}

func (s *Select) From(tables ...string) *Select {
	s.from = tables

	return s
}

func (s *Select) Where(where string) *Select {
	s.where = where

	return s
}

func (s *Select) OrderBy(orderBy string) *Select {
	s.orderBy = orderBy

	return s
}

func (s *Select) Compile() (string, error) {
	sb := new(strings.Builder)
	_, _ = sb.WriteString(s.stmt)
	_, _ = sb.WriteString(" ")
	if len(s.columns) > 0 {
		_, _ = sb.WriteString(strings.Join(s.columns, ","))
		_, _ = sb.WriteString(" ")
	} else {
		_, _ = sb.WriteString("* ")
	}
	if len(s.from) == 0 {
		return "", errors.New("no from specified")
	}
	_, _ = sb.WriteString("FROM ")
	_, _ = sb.WriteString(strings.TrimSpace(strings.Join(s.from, " ")))

	if len(s.where) > 0 {
		if !strings.HasPrefix(s.where, "WHERE") {
			_, _ = sb.WriteString(" WHERE ")
		} else {
			_, _ = sb.WriteString(" ")
		}
		_, _ = sb.WriteString(s.where)
	}
	if len(s.orderBy) > 0 {
		if !strings.HasPrefix(strings.TrimSpace(s.orderBy), "ORDER BY") {
			_, _ = sb.WriteString(" ORDER BY ")
		}
		_, _ = sb.WriteString(s.orderBy)
	}

	return sb.String(), nil
}

type Insert struct {
	stmt  string
	rows  string
	table string
}

func NewInsertInto(table string) *Insert {
	return &Insert{
		stmt:  InsertStatement,
		table: table,
	}
}

func (i *Insert) Rows(rows string) *Insert {
	i.rows = rows

	return i
}

func (i *Insert) Compile() (string, error) {
	sb := new(strings.Builder)
	_, _ = sb.WriteString(i.stmt)
	_, _ = sb.WriteString(" ")
	_, _ = sb.WriteString(i.table)
	_, _ = sb.WriteString(" ")
	if len(i.rows) == 0 {
		return "", errors.New("no rows in insert statement")
	}
	if !strings.HasPrefix(i.rows, "(") {
		_, _ = sb.WriteString("(")
	}
	_, _ = sb.WriteString(i.rows)
	if !strings.HasSuffix(i.rows, ")") {
		_, _ = sb.WriteString(")")
	}
	_, _ = sb.WriteString(" ")

	// count number of rows
	splitRows := strings.Split(i.rows, ",")
	_, _ = sb.WriteString("VALUES ")
	_, _ = sb.WriteString("($1")
	for i := 2; i <= len(splitRows); i++ {
		if _, err := fmt.Fprintf(sb, ", $%d", i); err != nil {
			return "", err
		}
	}
	_, _ = sb.WriteString(")")

	return sb.String(), nil
}

type Update struct {
	stmt  string
	table string
	rows  string
	where string
}

func NewUpdate(table string) *Update {
	return &Update{
		stmt:  UpdateStatement,
		table: table,
	}
}

func (u *Update) Set(rows string) *Update {
	u.rows = rows

	return u
}

func (u *Update) Where(where string) *Update {
	u.where = where

	return u
}

func (u *Update) Compile() (string, error) {
	counter := 1
	sb := new(strings.Builder)
	_, _ = sb.WriteString(u.stmt)
	_, _ = sb.WriteString(" ")
	if len(u.table) == 0 {
		return "", errors.New("no table specified")
	}
	_, _ = sb.WriteString(u.table)
	_, _ = sb.WriteString(" SET ")
	splitRows := strings.Split(u.rows, ",")
	for i, row := range splitRows {
		if _, err := fmt.Fprintf(sb, "%s = $%d", strings.TrimSpace(row), counter); err != nil {
			return "", err
		}
		if i < len(splitRows)-1 {
			_, _ = sb.WriteString(", ")
		}
		counter++
	}
	// sb.WriteString(" ")

	if len(u.where) > 0 {
		if !strings.HasPrefix(u.where, "WHERE") {
			_, _ = sb.WriteString(" WHERE ")
		}
		if !strings.Contains(u.where, "$") {
			splitWhere := strings.Split(u.where, ",")
			for i, row := range splitWhere {
				if _, err := fmt.Fprintf(sb, "%s = $%d", row, counter); err != nil {
					return "", err
				}
				if i < len(splitWhere)-1 {
					_, _ = sb.WriteString(" AND ")
				}
				counter++
			}
		} else {
			_, _ = sb.WriteString(u.where)
		}
	}

	return sb.String(), nil
}

type Delete struct {
	stmt  string
	table string
	where string
}

func NewDeleteFrom(table string) *Delete {
	return &Delete{
		stmt:  DeleteStatement,
		table: table,
	}
}

func (s *Delete) Where(where string) *Delete {
	s.where = where

	return s
}

func (s *Delete) Compile() (string, error) {
	sb := new(strings.Builder)
	_, _ = sb.WriteString(s.stmt)
	_, _ = sb.WriteString(" ")
	if len(s.table) == 0 {
		return "", errors.New("no table specified")
	}
	_, _ = sb.WriteString(s.table)
	if len(s.where) > 0 {
		if !strings.HasPrefix(s.where, "WHERE") {
			_, _ = sb.WriteString(" WHERE ")
		}
		_, _ = sb.WriteString(s.where)
	}

	return sb.String(), nil
}
