/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker

import "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/dedup"

// DeduplicateAndSort removes duplicate entries from a slice and sorts it.
func DeduplicateAndSort(source []string) []string {
	return dedup.AndSort(source)
}
