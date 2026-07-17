/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file provides generic utility functions including identity functions and nil checking.
// IdentityFunc returns a function that returns its input unchanged.
// IsNil checks if a value is nil using reflection for pointer-like types.

package utils

import "reflect"

func IdentityFunc[T any]() func(T) T {
	return func(t T) T { return t }
}

// IsNil returns true for nil values and interfaces containing typed nil values.
func IsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
