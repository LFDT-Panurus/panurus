/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	vault "github.com/hashicorp/vault/api"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

const (
	Keys         = "keys"
	Data         = "data"
	Value        = "value"
	CompositeKey = "\x00"
)

var (
	logger = logging.MustGetLogger()
)

type KVS struct {
	client *vault.Client
	path   string
}

// NewWithClient returns a new KVS instance for the passed hashicorp vault API client
func NewWithClient(client *vault.Client, path string) (*KVS, error) {
	// Add slash to the end of path if it is not exists
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	return &KVS{
		client: client,
		path:   path,
	}, nil
}

func (v *KVS) NormalizeID(id string) string {
	if strings.Contains(id, CompositeKey) {
		replaced := strings.ReplaceAll(id, CompositeKey, "/")
		replaced = strings.TrimPrefix(replaced, "/")
		replaced = strings.TrimPrefix(replaced, "/")
		id = strings.TrimSuffix(replaced, "/")
	}

	return v.path + id
}

func (v *KVS) deNormalizeID(id string) string {
	trimmedId := strings.TrimPrefix(id, v.path)
	trimmedId = strings.TrimPrefix(trimmedId, "/")
	trimmedId = strings.TrimSuffix(trimmedId, "/")
	normilzedId := "\x00" + strings.ReplaceAll(trimmedId, "/", CompositeKey) + "\x00"

	return normilzedId
}

func (v *KVS) GetExisting(ctx context.Context, ids ...string) []string {
	results := make([]string, 0)

	for _, id := range ids {
		if v.Exists(ctx, id) {
			results = append(results, id)
		}
	}

	return results
}

func (v *KVS) Exists(_ context.Context, id string) bool {
	id = v.NormalizeID(id)

	secret, err := v.client.Logical().Read(id)
	if err != nil {
		logger.Debugf("failed to check existence of id [%s]: %v", id, err)

		return false
	}

	if secret == nil || secret.Data == nil {
		logger.Debugf("state of id [%s] does not exist", id)

		return false
	}

	data, ok := secret.Data[Data].(map[string]any)
	if !ok || len(data) == 0 {
		logger.Debugf("state of id [%s] does not exist", id)

		return false
	}

	return true
}

func (v *KVS) Delete(_ context.Context, id string) error {
	id = v.NormalizeID(id)
	// Delete the secret from Vault
	_, err := v.client.Logical().Delete(id)
	if err != nil {
		return errors.Wrapf(err, "failed to delete state of id [%s]", id)
	}

	logger.Debugf("deleted state of id [%s] successfully", id)

	return nil
}

func (v *KVS) Close() error {
	return nil
}

func (v *KVS) Put(_ context.Context, id string, state any) error {
	id = v.NormalizeID(id)
	raw, err := json.Marshal(state)
	if err != nil {
		return errors.Wrapf(err, "cannot marshal state with id [%s]", id)
	}

	value := map[string]any{Value: base64.StdEncoding.EncodeToString(raw)}
	_, err = v.client.Logical().Write(id, map[string]any{Data: value})
	if err == nil {
		logger.Debugf("put state of id [%s] successfully", id)

		return nil
	}

	return errors.Wrapf(err, "failed to put state with id [%s]", id)
}

func (v *KVS) Get(_ context.Context, id string, state any) error {
	id = v.NormalizeID(id)
	secret, err := v.client.Logical().Read(id)
	if err != nil {
		return errors.Wrapf(err, "failed retrieving state of id [%s]", id)
	}

	if secret == nil {
		// In this case no value found for the input id
		return nil
	}

	if secret.Data == nil {
		return errors.Errorf("data should contain value for id [%s]", id)
	}

	data, _ := secret.Data[Data].(map[string]any)
	if len(data) == 0 {
		return errors.Errorf("state of id [%s] does not exist", id)
	}

	value, ok := data[Value]
	if !ok {
		return errors.Errorf("missing 'value' key in data")
	}
	raw, err := base64.StdEncoding.DecodeString(value.(string))
	if err != nil {
		logger.Debugf("Failed to decode base64 string: %v, error: %v", value, err)

		return errors.Wrapf(err, "failed to decode base64 string: %v", value)
	}

	if err := json.Unmarshal(raw, state); err != nil {
		logger.Debugf("failed retrieving state of id [%s], cannot unmarshal state, error [%s]", id, err)

		return errors.Wrapf(err, "failed retrieving state of id [%s], cannot unmarshal state", id)
	}
	logger.Debugf("got state of id [%s] successfully", id)

	return nil
}

func (v *KVS) GetByPartialCompositeID(_ context.Context, prefix string, attrs []string) (kvs.Iterator, error) {
	compositeKey, err := kvs.CreateCompositeKey(prefix, attrs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed building composite key for prefix [%s]", prefix)
	}

	compositeKey = v.NormalizeID(compositeKey)
	secret, err := v.client.Logical().List(compositeKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read list for key [%s]", compositeKey)
	}

	// No keys found
	if secret == nil {
		return nil, nil
	}

	// Check if the secret contains any keys
	if secret.Data == nil {
		return nil, errors.Errorf("secret contains no keys for prefix [%s]", prefix)
	}

	// Extract the keys from the response
	keys, ok := secret.Data[Keys].([]any)
	if !ok {
		return nil, errors.Errorf("unable to extract the keys from the response")
	}
	// Convert keys to []*string
	stringKeys := make([]*string, len(keys))
	for i, key := range keys {
		castedKey, ok := key.(string)
		if !ok {
			return nil, errors.Errorf("unable to cast key [%T]: ", key)
		}

		keyStr := v.deNormalizeID(compositeKey + "/" + castedKey)
		stringKeys[i] = &keyStr
	}
	// Create and return a sliceIterator for the keys
	keys_iterator := collections.NewSliceIterator(stringKeys)

	return &vaultIterator{ri: keys_iterator, client: v}, nil
}

type vaultIterator struct {
	ri     collections.Iterator[*string]
	next   *string
	client *KVS
}

func (i *vaultIterator) HasNext() bool {
	var err error
	i.next, err = i.ri.Next()
	if err != nil || i.next == nil {
		return false
	}

	return true
}

func (i *vaultIterator) Close() error {
	i.ri.Close()

	return nil
}

func (i *vaultIterator) Next(state any) (string, error) {
	if i.next == nil {
		return "", errors.Errorf("no more elements in the iterator")
	}
	err := i.client.Get(context.Background(), *i.next, state)

	return *i.next, err
}
