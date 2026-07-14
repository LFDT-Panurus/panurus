/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
)

// NoopSKIProvider returns an empty SKI.
type NoopSKIProvider struct{}

// NewNoopSKIProvider creates a new NoopSKIProvider.
func NewNoopSKIProvider() *NoopSKIProvider {
	return &NoopSKIProvider{}
}

// GetSKIsFromIdentity returns an empty slice.
func (p *NoopSKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	return nil, nil
}
