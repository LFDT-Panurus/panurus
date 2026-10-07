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
	"slices"
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

// GetExisting returns the subset of the passed ids that hold a state, in the order the ids
// were passed. Vault's KV v1 API has no multi-read endpoint, so this is one Exists round trip
// per id and, as in Exists, an id whose read fails is reported as absent rather than as an
// error.
func (v *KVS) GetExisting(ctx context.Context, ids ...string) []string {
	results := make([]string, 0)

	for _, id := range ids {
		if v.Exists(ctx, id) {
			results = append(results, id)
		}
	}

	return results
}

// Exists reports whether a state is stored under the passed id.
//
// The kvs.KVS interface gives this no error return, so a failed read is reported as "does not
// exist" — it is logged as an error rather than at debug level so that a storage problem is at
// least visible, instead of being indistinguishable from a genuine miss in the logs too.
// Callers that must tell the two apart have to read the state instead (see Get, and the way
// WalletStore.IdentityExists uses it).
func (v *KVS) Exists(ctx context.Context, id string) bool {
	id = v.NormalizeID(id)

	secret, err := v.client.Logical().ReadWithContext(ctx, id)
	if err != nil {
		logger.Errorf("failed to check existence of id [%s], reporting it as absent: %v", id, err)

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

// Delete removes the state stored under the passed id. Deleting an id that holds no state is
// not an error: Vault's delete does not report that distinction.
func (v *KVS) Delete(ctx context.Context, id string) error {
	id = v.NormalizeID(id)
	// Delete the secret from Vault
	_, err := v.client.Logical().DeleteWithContext(ctx, id)
	if err != nil {
		return errors.Wrapf(err, "failed to delete state of id [%s]", id)
	}

	logger.Debugf("deleted state of id [%s] successfully", id)

	return nil
}

// Close implements kvs.KVS and does nothing: the Vault API client this KVS reads and writes
// through is owned by whoever passed it to NewWithClient.
func (v *KVS) Close() error {
	return nil
}

// Put stores state under the passed id, replacing whatever was stored there before. The state
// is JSON-marshalled and the result base64-encoded, so that the Vault secret field holds it as
// an opaque string whatever bytes the marshalling produced.
func (v *KVS) Put(ctx context.Context, id string, state any) error {
	id = v.NormalizeID(id)
	raw, err := json.Marshal(state)
	if err != nil {
		return errors.Wrapf(err, "cannot marshal state with id [%s]", id)
	}

	value := map[string]any{Value: base64.StdEncoding.EncodeToString(raw)}
	_, err = v.client.Logical().WriteWithContext(ctx, id, map[string]any{Data: value})
	if err == nil {
		logger.Debugf("put state of id [%s] successfully", id)

		return nil
	}

	return errors.Wrapf(err, "failed to put state with id [%s]", id)
}

// Get unmarshals the state stored under the passed id into state.
//
// An id that holds no state is reported as success with state left untouched. That deviates
// from the FSC in-tree KVS, which returns a "does not exist" error that callers such as
// WalletStore.GetWalletID classify as an authoritative miss, so callers of this backend that
// must tell a miss apart from a stored zero value have to probe with Exists first.
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
//
// Every shape Vault has for "nothing of ours is stored here" is reported as absent rather
// than as an error, and they are: no secret at the path, a secret carrying no data at all,
// and a secret whose "data" field is missing, empty or not a map. They are one answer, and
// answering some of them with an error made a prefix scan abort on an entry it should have
// skipped - a secret written under the same mount by something other than this KVS has no
// "data" map and is reachable by any scan over that mount, KV v1 having one mount for every
// writer. Exists classifies the three the same way, so the two agree on what exists.
//
// A secret that does carry a "data" map is this KVS' own entry, so a "value" inside it that
// is missing, not a string or not decodable stays an error: that entry is corrupt, not
// absent, and reporting it as absent would hide the corruption from every caller.
func (v *KVS) read(ctx context.Context, id string) ([]byte, bool, error) {
	normalized := v.NormalizeID(id)
	secret, err := v.client.Logical().ReadWithContext(ctx, normalized)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed retrieving state of id [%s]", normalized)
	}

	if secret == nil || secret.Data == nil {
		// In this case no value found for the input id
		return nil, false, nil
	}

	data, ok := secret.Data[Data].(map[string]any)
	if !ok || len(data) == 0 {
		return nil, false, nil
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

// GetByPartialCompositeID returns an iterator over the states whose composite key starts with
// the passed prefix and attributes.
//
// The prefix and attributes are turned into a composite key and mapped onto this KVS' Vault
// path (see NormalizeID); listSubtree then collects the keys below it at every depth, and the
// iterator reads each of their states from Vault as it is advanced. A prefix with nothing
// under it yields an empty iterator rather than a nil one, so callers can iterate without a
// nil check.
//
// The states are yielded in ascending order of their composite key, which is the order the
// SQL backends' prefix scan uses, so the two are interchangeable for a caller that depends on
// scan order. Sorting happens after the Vault paths are mapped back onto composite keys, not
// on the paths themselves: NormalizeID joins the components with "/" where a composite key
// separates them with "\x00" and percent-escapes the ones that need it (a base64 identity
// hash routinely contains "/"), and both differences reorder keys relative to their raw form.
func (v *KVS) GetByPartialCompositeID(ctx context.Context, prefix string, attrs []string) (kvs.Iterator, error) {
	compositeKey, err := kvs.CreateCompositeKey(prefix, attrs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed building composite key for prefix [%s]", prefix)
	}

	paths, err := v.listSubtree(ctx, v.NormalizeID(compositeKey))
	if err != nil {
		return nil, err
	}

	keys := make([]string, len(paths))
	for i, path := range paths {
		key, err := v.deNormalizeID(path)
		if err != nil {
			return nil, err
		}
		keys[i] = key
	}
	slices.Sort(keys)

	refs := make([]*string, len(keys))
	for i := range keys {
		refs[i] = &keys[i]
	}

	return v.newIterator(ctx, refs), nil
}

// listSubtree returns the Vault paths of the keys that hold a state at or below the passed
// path, root excluded.
//
// Vault's list is single-level and reports a path that has children as an entry of its own
// with a trailing separator, so a prefix scan has to walk the subtree itself: the entries a
// caller scans for usually sit several components below the prefix, as
// WalletStore.GetConfID's scan of [tmsID] does for entries keyed by
// [tmsID, roleID, idHash, wID, "configid"]. Listing only the immediate children returns the
// directory markers instead, none of which holds a state, and the scan comes back empty.
//
// The walk is iterative so that a deep tree cannot exhaust the stack. A path can be both a
// leaf and a directory - Vault then lists it twice, once with and once without the trailing
// separator - which is why a directory contributes no path of its own and no entry is
// returned twice.
//
// The order of the returned paths is unspecified: a depth-first walk produces them in an order
// that depends on the shape of the tree. GetByPartialCompositeID is what makes a scan
// reproducible, by sorting the composite keys these paths map back onto.
func (v *KVS) listSubtree(ctx context.Context, root string) ([]string, error) {
	var paths []string

	pending := []string{root}
	for len(pending) > 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		children, err := v.list(ctx, dir)
		if err != nil {
			return nil, err
		}

		for _, child := range children {
			path := strings.TrimSuffix(dir, pathSeparator) + pathSeparator + child
			if strings.HasSuffix(child, pathSeparator) {
				pending = append(pending, path)

				continue
			}

			paths = append(paths, path)
		}
	}

	return paths, nil
}

// list returns the names of the immediate children of the passed Vault path, directories
// included and still carrying their trailing separator.
//
// A path with no children lists as a nil secret, and so does one that was deleted between its
// parent's list and this call, so neither is an error: both are reported as no children.
func (v *KVS) list(ctx context.Context, path string) ([]string, error) {
	secret, err := v.client.Logical().ListWithContext(ctx, path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read list for key [%s]", path)
	}
	if secret == nil || secret.Data == nil {
		return nil, nil
	}

	raw, ok := secret.Data[Keys].([]any)
	if !ok {
		return nil, errors.Errorf("unable to extract the keys from the response for key [%s]", path)
	}

	children := make([]string, len(raw))
	for i, key := range raw {
		castedKey, ok := key.(string)
		if !ok {
			return nil, errors.Errorf("unable to cast key [%T] of the list for key [%s]", key, path)
		}
		children[i] = castedKey
	}

	return children, nil
}

// vaultIterator iterates over the keys a Vault list returned, reading each key's state from
// Vault as it goes. Vault's KV v1 API has no multi-read endpoint, so there is one round trip
// per key; the read happens in HasNext rather than in Next so that a key deleted between the
// list and its read can be skipped instead of being yielded as a zero-valued state.
//
// The lookahead HasNext performs is memoized, so that calling HasNext repeatedly before Next
// neither repeats the read nor drops the key it already advanced to.
type vaultIterator struct {
	ri     collections.Iterator[*string]
	client *KVS
	//nolint:containedctx // kvs.Iterator has no context parameter, so the caller's context has
	// to be carried to the reads HasNext performs on its behalf
	ctx context.Context

	// loaded reports whether the fields below hold the lookahead of a HasNext call that Next
	// has not consumed yet.
	loaded  bool
	hasNext bool
	key     string
	raw     []byte
	err     error
}

func (v *KVS) newIterator(ctx context.Context, keys []*string) *vaultIterator {
	return &vaultIterator{ri: collections.NewSliceIterator(keys), client: v, ctx: ctx}
}

func (i *vaultIterator) HasNext() bool {
	if i.loaded {
		return i.hasNext
	}
	i.loaded = true

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
	if !i.loaded || !i.hasNext {
		return "", errors.Errorf("no more elements in the iterator")
	}
	i.loaded, i.hasNext = false, false

	if i.err != nil {
		return i.key, i.err
	}

	return i.key, unmarshalState(i.key, i.raw, state)
}
