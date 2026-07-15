/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package translator

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type (
	Key       = string
	Value     = []byte
	TxID      = string
	Namespace = string
)

//go:generate counterfeiter -o mock/rwset.go -fake-name RWSet . RWSet

// KeyTranslator is used to translate tokens' concepts into backend's keys.
type KeyTranslator interface {
	// CreateTokenRequestKey creates the key for a token request with the passed id
	CreateTokenRequestKey(id string) (Key, error)
	// CreateSetupKey creates the key for public parameters
	CreateSetupKey() (Key, error)
	// CreateSetupHashKey creates the key for the hashed public parameters
	CreateSetupHashKey() (Key, error)
	// CreateOutputKey creates the key for an output
	CreateOutputKey(id string, index uint64) (Key, error)
	// CreateOutputSNKey creates the key for the serial number of an output
	CreateOutputSNKey(id string, index uint64, output []byte) (Key, error)
	// CreateInputSNKey creates the key for the serial number of an input
	CreateInputSNKey(id string) (Key, error)
	// CreateIssueActionMetadataKey returns the issue action metadata key built from the passed key
	CreateIssueActionMetadataKey(key string) (Key, error)
	// CreateTransferActionMetadataKey returns the transfer action metadata key built from the passed subkey
	CreateTransferActionMetadataKey(subkey string) (Key, error)
	// GetTransferMetadataSubKey returns the subkey in the given transfer action metadata key
	GetTransferMetadataSubKey(k string) (Key, error)
	// TransferActionMetadataKeyPrefix returns the key prefix shared by all transfer action metadata
	// keys, i.e., the same prefix produced by CreateTransferActionMetadataKey with no subkey. It is
	// used to prefix-match transfer action metadata writes without knowing a specific subkey.
	TransferActionMetadataKeyPrefix() (Key, error)
}

// RWSet interface, used to read from, and write to, a rwset.
type RWSet interface {
	SetState(namespace string, key string, value []byte) error
	GetState(namespace string, key string) ([]byte, error)
	DeleteState(namespace string, key string) error
}

// KeyVersion models the concept of a specific key version as `version zero` or `any`.
type KeyVersion = int

const (
	// Latest value, latest version of the key known to the node
	Latest KeyVersion = iota
	// VersionZero value,  version `zero` of the key
	VersionZero
)

// ExRWSet interface, used to manipulate the rwset in a more friendly way
type ExRWSet interface {
	// SetState adds a write entry to the rwset that write to given value to given key.
	SetState(key Key, value Value) error
	// GetState returns the value bound to the passed key
	GetState(key Key) ([]byte, error)
	// DeleteState adds a write entry to the rwset that deletes the passed key
	DeleteState(key Key) error
	// StateMustNotExist adds a read dependency that enforces that the passed key does not exist
	StateMustNotExist(key Key) error
	// StateMustExist adds a read dependency that enforces that the passed key does exist
	// When using VersionZero, this method should be called only for keys that are guaranteed to be used only once.
	StateMustExist(key Key, version KeyVersion) error
}

type RWSetWrapper struct {
	RWSet     RWSet
	Namespace Namespace
	TxID      TxID
}

func NewRWSetWrapper(RWSet RWSet, namespace Namespace, txID TxID) *RWSetWrapper {
	return &RWSetWrapper{RWSet: RWSet, Namespace: namespace, TxID: txID}
}

func (w *RWSetWrapper) SetState(key Key, value Value) error {
	return w.RWSet.SetState(w.Namespace, key, value)
}

func (w *RWSetWrapper) GetState(key Key) (Value, error) {
	return w.RWSet.GetState(w.Namespace, key)
}

func (w *RWSetWrapper) DeleteState(key Key) error {
	return w.RWSet.DeleteState(w.Namespace, key)
}

func (w *RWSetWrapper) StateMustNotExist(key Key) error {
	tr, err := w.RWSet.GetState(w.Namespace, key)
	if err != nil {
		return errors.Wrapf(err, "failed to read state [%s:%s] for [%s]", w.Namespace, key, w.TxID)
	}
	if len(tr) != 0 {
		return errors.Errorf("state [%s:%s] already exists for [%s]", w.Namespace, key, w.TxID)
	}

	return nil
}

func (w *RWSetWrapper) StateMustExist(key Key, version KeyVersion) error {
	// This wrapper assumes the following:
	// When StateMustExist is called on VersionZero, Latest behaviour is used instead.
	// This works under the assumption that keys are used only once.
	h, err := w.RWSet.GetState(w.Namespace, key)
	if err != nil {
		return errors.Wrapf(err, "failed to read state [%s:%s] for [%s]", w.Namespace, key, w.TxID)
	}
	if len(h) == 0 {
		return errors.Errorf("state [%s:%s] does not exist for [%s]", w.Namespace, key, w.TxID)
	}

	return nil
}

type HashedKeyTranslator struct {
	KT KeyTranslator
}

func (h *HashedKeyTranslator) CreateTokenRequestKey(id string) (Key, error) {
	k, err := h.KT.CreateTokenRequestKey(id)
	if err != nil {
		return "", err
	}

	return h.hash(0, k)
}

func (h *HashedKeyTranslator) CreateSetupKey() (Key, error) {
	k, err := h.KT.CreateSetupKey()
	if err != nil {
		return "", err
	}

	return h.hash(1, k)
}

func (h *HashedKeyTranslator) CreateSetupHashKey() (Key, error) {
	k, err := h.KT.CreateSetupHashKey()
	if err != nil {
		return "", err
	}

	return h.hash(2, k)
}

func (h *HashedKeyTranslator) CreateOutputSNKey(id string, index uint64, output []byte) (Key, error) {
	k, err := h.KT.CreateOutputSNKey(id, index, output)
	if err != nil {
		return "", err
	}

	return h.hash(3, k)
}

func (h *HashedKeyTranslator) CreateOutputKey(id string, index uint64) (Key, error) {
	k, err := h.KT.CreateOutputKey(id, index)
	if err != nil {
		return "", err
	}

	return h.hash(4, k)
}

func (h *HashedKeyTranslator) GetTransferMetadataSubKey(k string) (Key, error) {
	key, err := h.KT.GetTransferMetadataSubKey(k)
	if err != nil {
		return "", err
	}

	return h.hash(5, key)
}

func (h *HashedKeyTranslator) CreateInputSNKey(id string) (Key, error) {
	k, err := h.KT.CreateInputSNKey(id)
	if err != nil {
		return "", err
	}

	return h.hash(6, k)
}

func (h *HashedKeyTranslator) CreateIssueActionMetadataKey(key Key) (Key, error) {
	k, err := h.KT.CreateIssueActionMetadataKey(key)
	if err != nil {
		return "", err
	}

	return h.hash(7, k)
}

func (h *HashedKeyTranslator) CreateTransferActionMetadataKey(key Key) (Key, error) {
	k, err := h.KT.CreateTransferActionMetadataKey(key)
	if err != nil {
		return "", err
	}

	return h.hash(8, k)
}

func (h *HashedKeyTranslator) TransferActionMetadataKeyPrefix() (Key, error) {
	// TODO:
	return "", nil
}

func (h *HashedKeyTranslator) hash(code byte, k string) (Key, error) {
	hf := sha256.New()
	hf.Write([]byte{code})
	hf.Write([]byte(k))

	return hex.EncodeToString(hf.Sum(nil)), nil
}
