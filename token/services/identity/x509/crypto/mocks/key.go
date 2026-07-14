/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mocks

import "github.com/hyperledger/fabric-lib-go/bccsp"

type Key struct{}

func (t *Key) Bytes() ([]byte, error) {
	panic("implement me")
}

func (t *Key) SKI() []byte {
	panic("implement me")
}

func (t *Key) Symmetric() bool {
	panic("implement me")
}

func (t *Key) Private() bool {
	panic("implement me")
}

func (t *Key) PublicKey() (bccsp.Key, error) {
	panic("implement me")
}
