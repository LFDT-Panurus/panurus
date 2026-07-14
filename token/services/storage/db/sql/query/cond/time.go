/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

type cmpDuration struct {
	field    common.Serializable
	operator string
	duration time.Duration
}

func InPast(field common.Serializable) Condition {
	return &cmpDuration{field: field, operator: " < ", duration: 0}
}

func InFuture(field common.Serializable) Condition {
	return &cmpDuration{field: field, operator: " > ", duration: 0}
}

func OlderThan(field common.Serializable, duration time.Duration) Condition {
	return &cmpDuration{field: field, operator: " < ", duration: -duration}
}

func NewerThan(field common.Serializable, duration time.Duration) Condition {
	return &cmpDuration{field: field, operator: " > ", duration: -duration}
}

func BeforeNext(field common.Serializable, duration time.Duration) Condition {
	return &cmpDuration{field: field, operator: " < ", duration: duration}
}

func AfterNext(field common.Serializable, duration time.Duration) Condition {
	return &cmpDuration{field: field, operator: " > ", duration: duration}
}

func (c *cmpDuration) WriteString(in common.CondInterpreter, sb common.Builder) {
	sb.WriteSerializables(c.field).WriteString(c.operator)
	in.TimeOffset(c.duration, sb)
}
