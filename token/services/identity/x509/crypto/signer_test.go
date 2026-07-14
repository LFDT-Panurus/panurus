/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewSKIBasedSigner(t *testing.T) {
	tests := []struct {
		name    string
		csp     bccsp.BCCSP
		ski     []byte
		pk      crypto.PublicKey
		wantErr bool
	}{
		{
			name:    "Invalid CSP",
			csp:     nil,
			ski:     []byte("test"),
			pk:      &testPublicKey{},
			wantErr: true,
		},
		{
			name:    "Empty SKI",
			csp:     &mocks.BCCSP{},
			ski:     []byte{},
			pk:      &testPublicKey{},
			wantErr: true,
		},
		{
			name:    "Nil PK",
			csp:     &mocks.BCCSP{},
			ski:     []byte("test"),
			pk:      nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSKIBasedSigner(tt.csp, tt.ski, tt.pk)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSKIBasedSigner_Public(t *testing.T) {
	pk := &testPublicKey{}
	signer, err := NewSKIBasedSigner(&mocks.BCCSP{}, []byte("test"), pk)
	require.NoError(t, err)

	actualPk := signer.Public()
	assert.Equal(t, pk, actualPk)
}

func TestSKIBasedSigner_Sign(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (crypto.Signer, *mocks.BCCSP)
		digest  []byte
		opts    crypto.SignerOpts
		wantErr bool
		signer  crypto.Signer
	}{
		{
			name: "Success",
			setup: func() (crypto.Signer, *mocks.BCCSP) {
				mockCsp := &mocks.BCCSP{}
				signer, err := NewSKIBasedSigner(mockCsp, []byte("test"), &testPublicKey{})
				require.NoError(t, err)
				mockCsp.On("GetKey", mock.Anything).Return(&mocks.Key{}, nil)
				mockCsp.On("Sign", mock.Anything, mock.Anything, mock.Anything).Return([]byte("signature"), nil)

				return signer, mockCsp
			},
			digest:  []byte("digest"),
			opts:    nil,
			wantErr: false,
		},
		{
			name: "GetKey Failure",
			setup: func() (crypto.Signer, *mocks.BCCSP) {
				mockCsp := &mocks.BCCSP{}
				signer, err := NewSKIBasedSigner(mockCsp, []byte("test"), &testPublicKey{})
				require.NoError(t, err)
				mockCsp.On("GetKey", mock.Anything).Return(nil, errors.New("get key failed"))

				return signer, mockCsp
			},
			digest:  []byte("digest"),
			opts:    nil,
			wantErr: true,
		},
		{
			name: "Sign Failure",
			setup: func() (crypto.Signer, *mocks.BCCSP) {
				mockCsp := &mocks.BCCSP{}
				signer, err := NewSKIBasedSigner(mockCsp, []byte("test"), &testPublicKey{})
				require.NoError(t, err)
				mockCsp.On("GetKey", mock.Anything).Return(&mocks.Key{}, nil)
				mockCsp.On("Sign", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("sign failed"))

				return signer, mockCsp
			},
			digest:  []byte("digest"),
			opts:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, mockCsp := tt.setup()
			_, err := signer.Sign(nil, tt.digest, tt.opts)
			mockCsp.AssertExpectations(t)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSKIBasedSigner_SignFull(t *testing.T) {
	csp, err := GetDefaultBCCSP(csp.NewKVSStore(kvs.NewTrackedMemory()))
	require.NoError(t, err)

	key, err := csp.KeyGen(&bccsp.ECDSAKeyGenOpts{})
	require.NoError(t, err)

	signer, err := NewSKIBasedSigner(csp, key.SKI(), &ecdsa.PublicKey{})
	require.NoError(t, err)

	message := []byte("message")
	sigma, err := signer.Sign(nil, message, nil)
	require.NoError(t, err)
	assert.NotNil(t, sigma)

	pk, err := key.PublicKey()
	require.NoError(t, err)

	valid, err := csp.Verify(pk, sigma, message, nil)
	require.NoError(t, err)
	assert.True(t, valid)
}

// Helper types for testing
type testPublicKey struct{}
