/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

// Zero returns the zero value for type A.
func Zero[A any]() A {
	var a A

	return a
}

// MustGet returns v, panicking if err is non-nil.
func MustGet[V any](v V, err error) V {
	if err != nil {
		panic(err)
	}

	return v
}
