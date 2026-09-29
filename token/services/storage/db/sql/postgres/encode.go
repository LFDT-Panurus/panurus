/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

// identity is the value decoder of a primary key that needs no decoding: the
// notification payload already carries the column's text representation.
//
// A decodeBYTEA decoder, reachable only through a NewBytePrimaryKey
// constructor, used to live here as well. Neither had a call site: every
// notifier in the tree declares its primary keys with NewSimplePrimaryKey, and
// the trigger renders payload values with ::text, which for a bytea column
// yields the hex form that a BYTEA decoder would have had to undo. Both were
// removed in #2043 rather than kept as tested but unreachable code; a notifier
// on a bytea primary key can reintroduce a decoder together with its caller.
func identity(a string) (string, error) { return a, nil }
