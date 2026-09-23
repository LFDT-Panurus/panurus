/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
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

	// pathSeparator separates the components of a composite key once it is mapped onto a
	// Vault path.
	pathSeparator = "/"
	// emptyComponent encodes an empty composite-key component. url.PathEscape never emits a
	// bare '%' (it only ever writes one as the first byte of a "%XX" triplet), so no escaped
	// non-empty component can collide with it.
	emptyComponent = "%"
	// escapedDot encodes the dots of a "." or ".." component. Those two are left untouched by
	// url.PathEscape, but the Vault client passes the request path through path.Join, which
	// would resolve them away and let a component walk out of this KVS' path prefix.
	escapedDot = "%2E"
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

// NormalizeID maps the passed id onto the Vault path this KVS is rooted at.
//
// A composite key (see kvs.CreateCompositeKey) becomes one Vault path component per key
// component, each of them percent-escaped. The escaping is what makes the mapping injective:
// a raw component that is empty, or that contains pathSeparator - base64-encoded identity
// hashes routinely do - would otherwise drop or add path components and let two distinct
// composite keys resolve to the same Vault path, for example CreateCompositeKey("",
// []string{"1"}) and CreateCompositeKey("1", nil). It also keeps a "." or ".." component from
// walking out of this KVS' path prefix. Ids that are not composite keys are appended as they
// are.
func (v *KVS) NormalizeID(id string) string {
	if !strings.Contains(id, CompositeKey) {
		return v.path + id
	}

	components := splitCompositeKey(id)
	escaped := make([]string, len(components))
	for i, component := range components {
		escaped[i] = escapeComponent(component)
	}

	return v.path + strings.Join(escaped, pathSeparator)
}

// deNormalizeID is the inverse of NormalizeID for composite keys: it maps a Vault path back
// onto the composite key it was built from.
func (v *KVS) deNormalizeID(id string) (string, error) {
	trimmed := strings.TrimPrefix(id, v.path)
	trimmed = strings.TrimPrefix(trimmed, pathSeparator)
	// Vault reports a path that has children with a trailing separator. An empty component is
	// encoded as emptyComponent, so trimming it never drops one.
	trimmed = strings.TrimSuffix(trimmed, pathSeparator)

	var sb strings.Builder
	sb.WriteString(CompositeKey)
	for component := range strings.SplitSeq(trimmed, pathSeparator) {
		unescaped, err := unescapeComponent(component)
		if err != nil {
			return "", errors.Wrapf(err, "failed to decode component [%s] of vault path [%s]", component, id)
		}
		sb.WriteString(unescaped)
		sb.WriteString(CompositeKey)
	}

	return sb.String(), nil
}

// splitCompositeKey splits a composite key into its components, the object type followed by
// the attributes. kvs.CreateCompositeKey prefixes the key with one delimiter and terminates
// every component with one, so exactly one leading and one trailing delimiter are dropped;
// trimming any more of them would merge an empty component into its neighbour.
func splitCompositeKey(id string) []string {
	id = strings.TrimSuffix(strings.TrimPrefix(id, CompositeKey), CompositeKey)

	return strings.Split(id, CompositeKey)
}

// escapeComponent encodes a single composite-key component as one Vault path component.
func escapeComponent(component string) string {
	switch component {
	case "":
		return emptyComponent
	case ".", "..":
		return strings.ReplaceAll(component, ".", escapedDot)
	default:
		return url.PathEscape(component)
	}
}

// unescapeComponent is the inverse of escapeComponent.
func unescapeComponent(component string) (string, error) {
	if component == emptyComponent {
		return "", nil
	}

	return url.PathUnescape(component)
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

func (v *KVS) Get(ctx context.Context, id string, state any) error {
	raw, found, err := v.read(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		// In this case no value found for the input id
		return nil
	}

	return unmarshalState(id, raw, state)
}

// read returns the still-marshalled state stored under the passed id, together with whether
// the id exists at all. Telling a missing id apart from a stored one is what lets the
// iterator skip keys that are deleted between a list and their read, instead of handing back
// an untouched, zero-valued state as if it were stored data.
func (v *KVS) read(_ context.Context, id string) ([]byte, bool, error) {
	normalized := v.NormalizeID(id)
	secret, err := v.client.Logical().Read(normalized)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed retrieving state of id [%s]", normalized)
	}

	if secret == nil {
		// In this case no value found for the input id
		return nil, false, nil
	}

	if secret.Data == nil {
		return nil, false, errors.Errorf("data should contain value for id [%s]", normalized)
	}

	data, _ := secret.Data[Data].(map[string]any)
	if len(data) == 0 {
		return nil, false, errors.Errorf("state of id [%s] does not exist", normalized)
	}

	value, ok := data[Value]
	if !ok {
		return nil, false, errors.Errorf("missing 'value' key in data")
	}
	encoded, ok := value.(string)
	if !ok {
		return nil, false, errors.Errorf("value of id [%s] is not a string but a [%T]", normalized, value)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		logger.Debugf("Failed to decode base64 string: %v, error: %v", value, err)

		return nil, false, errors.Wrapf(err, "failed to decode base64 string: %v", value)
	}

	return raw, true, nil
}

// unmarshalState unmarshals a state read by read into the caller's destination.
func unmarshalState(id string, raw []byte, state any) error {
	if err := json.Unmarshal(raw, state); err != nil {
		logger.Debugf("failed retrieving state of id [%s], cannot unmarshal state, error [%s]", id, err)

		return errors.Wrapf(err, "failed retrieving state of id [%s], cannot unmarshal state", id)
	}
	logger.Debugf("got state of id [%s] successfully", id)

	return nil
}

func (v *KVS) GetByPartialCompositeID(ctx context.Context, prefix string, attrs []string) (kvs.Iterator, error) {
	compositeKey, err := kvs.CreateCompositeKey(prefix, attrs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed building composite key for prefix [%s]", prefix)
	}

	compositeKey = v.NormalizeID(compositeKey)
	secret, err := v.client.Logical().List(compositeKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read list for key [%s]", compositeKey)
	}

	// No keys found: hand back an empty iterator rather than a nil one, so that callers can
	// iterate without a nil check.
	if secret == nil {
		return v.newIterator(ctx, nil), nil
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

		keyStr, err := v.deNormalizeID(compositeKey + pathSeparator + castedKey)
		if err != nil {
			return nil, err
		}
		stringKeys[i] = &keyStr
	}

	return v.newIterator(ctx, stringKeys), nil
}

// vaultIterator iterates over the keys a Vault list returned, reading each key's state from
// Vault as it goes. Vault's KV v1 API has no multi-read endpoint, so there is one round trip
// per key; the read happens in HasNext rather than in Next so that a key deleted between the
// list and its read can be skipped instead of being yielded as a zero-valued state.
type vaultIterator struct {
	ri     collections.Iterator[*string]
	client *KVS
	//nolint:containedctx // kvs.Iterator has no context parameter, so the caller's context has
	// to be carried to the reads HasNext performs on its behalf
	ctx context.Context

	hasNext bool
	key     string
	raw     []byte
	err     error
}

func (v *KVS) newIterator(ctx context.Context, keys []*string) *vaultIterator {
	return &vaultIterator{ri: collections.NewSliceIterator(keys), client: v, ctx: ctx}
}

func (i *vaultIterator) HasNext() bool {
	for {
		key, err := i.ri.Next()
		if err != nil || key == nil {
			i.hasNext = false

			return false
		}

		raw, found, err := i.client.read(i.ctx, *key)
		if err != nil {
			// Keep the key and surface the failure from Next, which can report it.
			i.key, i.raw, i.err, i.hasNext = *key, nil, err, true

			return true
		}
		if !found {
			// The key was deleted between the list and this read: skip it, rather than
			// yielding a zero-valued state as if it were stored data.
			logger.Debugf("skipping id [%s], it is no longer available", *key)

			continue
		}

		i.key, i.raw, i.err, i.hasNext = *key, raw, nil, true

		return true
	}
}

func (i *vaultIterator) Close() error {
	i.ri.Close()

	return nil
}

func (i *vaultIterator) Next(state any) (string, error) {
	if !i.hasNext {
		return "", errors.Errorf("no more elements in the iterator")
	}
	i.hasNext = false

	if i.err != nil {
		return i.key, i.err
	}

	return i.key, unmarshalState(i.key, i.raw, state)
}
