/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package meta

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActionMetadata(t *testing.T) {
	attrs := map[string]any{
		"TransferMetadataPrefixfoo": []byte("bar"),
		"TransferMetadataPrefixbaz": []byte("qux"),
		"IssueMetadataPrefixabc":    []byte("def"),
		"TransferMetadataPrefixbad": "not a []byte", // should be ignored
	}

	expectedTransfer := map[string][]byte{
		"foo": []byte("bar"),
		"baz": []byte("qux"),
	}
	expectedIssue := map[string][]byte{
		"abc": []byte("def"),
	}

	transfer := TransferActionMetadata(attrs)
	assert.Equal(t, expectedTransfer, transfer)

	issue := IssueActionMetadata(attrs)
	assert.Equal(t, expectedIssue, issue)

	// Test ActionMetadata directly with a custom prefix
	attrs2 := map[string]any{
		"CustomPrefixkey": []byte("val"),
		"CustomPrefixx":   []byte("y"),
		"OtherPrefixz":    []byte("should be ignored"),
	}
	expectedCustom := map[string][]byte{
		"key": []byte("val"),
		"x":   []byte("y"),
	}
	custom := ActionMetadata(attrs2, "CustomPrefix")
	assert.Equal(t, expectedCustom, custom)

	// Test empty attrs
	empty := ActionMetadata(map[string]any{}, "AnyPrefix")
	assert.Empty(t, empty)
}
