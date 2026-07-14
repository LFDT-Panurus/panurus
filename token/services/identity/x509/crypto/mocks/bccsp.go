/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mocks

import (
	"hash"

	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/mock"
)

type BCCSP struct {
	mock.Mock
}

func (m *BCCSP) KeyGen(opts bccsp.KeyGenOpts) (k bccsp.Key, err error) {
	panic("implement me")
}

func (m *BCCSP) KeyDeriv(k bccsp.Key, opts bccsp.KeyDerivOpts) (dk bccsp.Key, err error) {
	panic("implement me")
}

func (m *BCCSP) KeyImport(raw any, opts bccsp.KeyImportOpts) (k bccsp.Key, err error) {
	panic("implement me")
}

func (m *BCCSP) Hash(msg []byte, opts bccsp.HashOpts) (hash []byte, err error) {
	panic("implement me")
}

func (m *BCCSP) GetHash(opts bccsp.HashOpts) (h hash.Hash, err error) {
	panic("implement me")
}

func (m *BCCSP) Verify(k bccsp.Key, signature, digest []byte, opts bccsp.SignerOpts) (valid bool, err error) {
	panic("implement me")
}

func (m *BCCSP) Encrypt(k bccsp.Key, plaintext []byte, opts bccsp.EncrypterOpts) (ciphertext []byte, err error) {
	panic("implement me")
}

func (m *BCCSP) Decrypt(k bccsp.Key, ciphertext []byte, opts bccsp.DecrypterOpts) (plaintext []byte, err error) {
	panic("implement me")
}

func (m *BCCSP) GetKey(ski []byte) (bccsp.Key, error) {
	args := m.Called(ski)
	err := args.Error(1)
	if err != nil {
		return nil, err
	}

	return args.Get(0).(bccsp.Key), args.Error(1)
}

func (m *BCCSP) Sign(k bccsp.Key, digest []byte, opts bccsp.SignerOpts) ([]byte, error) {
	args := m.Called(k, digest, opts)
	err := args.Error(1)
	if err != nil {
		return nil, err
	}

	return args.Get(0).([]byte), args.Error(1)
}
