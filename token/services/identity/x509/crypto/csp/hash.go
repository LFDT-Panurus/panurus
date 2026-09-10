/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"hash"

	"github.com/hyperledger/fabric-lib-go/bccsp"
)

type hasher struct {
	hash func() hash.Hash
}

func (c *hasher) Hash(msg []byte, opts bccsp.HashOpts) ([]byte, error) {
	h := c.hash()
	_, _ = h.Write(msg)

	return h.Sum(nil), nil
}

func (c *hasher) GetHash(opts bccsp.HashOpts) (hash.Hash, error) {
	return c.hash(), nil
}
