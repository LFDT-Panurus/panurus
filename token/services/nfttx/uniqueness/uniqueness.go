/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package uniqueness

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/marshaller"
)

// KVS is the interface for the key-value store backend used by the uniqueness service.
type KVS interface {
	Exists(ctx context.Context, k string) bool
	Get(ctx context.Context, k string, v any) error
	Put(ctx context.Context, k string, key any) error
}

// Service is a uniqueness service.
// The service computes a unique id for a given object by hashing the object's json representation together with a random salt.
// The random salt is used to avoid dictionary attacks.
// The random salt is stored in the KVS and is generated on the first call to the service.
type Service struct {
	mutex sync.Mutex
	kvs   KVS
}

// NewService creates a new uniqueness service with the given KVS backend.
func NewService(kvs KVS) *Service {
	return &Service{kvs: kvs}
}

// ComputeID computes the unique ID of the given object.
func (s *Service) ComputeID(ctx context.Context, state any) (string, error) {
	if state == nil {
		return "", errors.New("state is nil")
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	k := "github.com/LFDT-Panurus/panurus/token/services/nfttx/uniqueness/key"
	var key []byte
	if s.kvs.Exists(ctx, k) {
		if err := s.kvs.Get(ctx, k, &key); err != nil {
			return "", errors.WithMessagef(err, "failed to get key %s", k)
		}
	} else {
		// sample a random 32 bytes key and store it
		size := 32
		key = make([]byte, size)
		n, err := rand.Read(key)
		if err != nil {
			return "", errors.Wrap(err, "error getting random bytes")
		}
		if n != size {
			return "", errors.New("error getting random bytes")
		}
		if err := s.kvs.Put(ctx, k, key); err != nil {
			return "", errors.WithMessagef(err, "failed to put key %s", k)
		}
	}
	raw, err := marshaller.Marshal(state)
	if err != nil {
		return "", errors.Wrapf(err, "failed to marshal state")
	}
	hash := sha256.New()
	n, err := hash.Write(raw)
	if n != len(raw) {
		return "", errors.New("error writing to hash")
	}
	if err != nil {
		return "", errors.Wrapf(err, "error writing to hash")
	}
	digest := hash.Sum(nil)

	return base64.StdEncoding.EncodeToString(digest), nil
}

// GetService returns the uniqueness service.
func GetService(sp token.ServiceProvider) *Service {
	s, err := sp.GetService(&Service{})
	if err != nil {
		panic(err)
	}

	return s.(*Service)
}
