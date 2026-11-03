/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"encoding/hex"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
)

type serializer interface {
	marshall() ([]byte, error)
	unmarshall(raw []byte) error
}

type KeyEntry struct {
	KeyType string
	Raw     []byte
}

type KVS interface {
	Put(id string, state any) error
	Get(id string, state any) error
}

type KVSStore struct {
	KVS
}

func NewKVSStore(KVS KVS) *KVSStore {
	return &KVSStore{KVS: KVS}
}

// ReadOnly returns true if this KeyStore is read only, false otherwise.
// If ReadOnly is true then StoreKey will fail.
func (ks *KVSStore) ReadOnly() bool {
	return false
}

// GetKey returns a key object whose SKI is the one passed.
func (ks *KVSStore) GetKey(ski []byte) (bccsp.Key, error) {
	id := hex.EncodeToString(ski)

	value := &KeyEntry{}
	err := ks.Get(id, value)
	if err != nil {
		return nil, errors.Wrapf(err, "could not get key [%s] from kvs", id)
	}

	switch value.KeyType {
	case "ecdsaPrivateKey":
		privateKey := &ecdsaPrivateKey{}
		err = privateKey.unmarshall(value.Raw)
		if err != nil {
			return nil, errors.WithMessagef(err, "could not unmarshall ECDSA private key")
		}

		return privateKey, nil
	case "ecdsaPublicKey":
		publicKey := &ecdsaPublicKey{}
		err = publicKey.unmarshall(value.Raw)
		if err != nil {
			return nil, errors.WithMessagef(err, "could not unmarshall ECDSA public key")
		}

		return publicKey, nil
	default:
		return nil, errors.Errorf("key not found for [%s]", id)
	}
}

// StoreKey stores the key k in this KeyStore.
// If this KeyStore is read only then the method will fail.
func (ks *KVSStore) StoreKey(k bccsp.Key) error {
	value := &KeyEntry{}
	var id string

	ser, ok := k.(serializer)
	if !ok {
		return errors.Errorf("key type not supported [%s]", k)
	}

	switch key := k.(type) {
	case *ecdsaPrivateKey:
		value.KeyType = "ecdsaPrivateKey"
		pk, err := k.PublicKey()
		if err != nil {
			return errors.Errorf("could not get public version for key [%s]", k.SKI())
		}
		id = hex.EncodeToString(pk.SKI())
	case *ecdsaPublicKey:
		value.KeyType = "ecdsaPublicKey"
		id = hex.EncodeToString(k.SKI())
	default:
		return errors.Errorf("unknown type [%T] for the supplied key", key)
	}
	if len(id) == 0 {
		// SKI() is empty when ECDH() fails (e.g. P-224)
		return errors.Errorf("cannot store a key with an empty SKI [%T]", k)
	}
	var err error
	value.Raw, err = ser.marshall()
	if err != nil {
		return errors.Wrapf(err, "could not marshall stored key [%s]", id)
	}

	return ks.Put(id, value)
}
