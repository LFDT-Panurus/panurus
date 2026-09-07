/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade

import (
	"context"
	"slices"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrPublicParamsNotFound reports that the store holds no public parameters under a hash.
// It is distinct from public parameters that are stored but unusable, or that do not generate
// the format they were looked up for: only a genuine absence lets a caller fall back to
// resolving the generation by format.
var ErrPublicParamsNotFound = errors.New("public parameters not found")

// PublicParamsStore gives read access to every generation of public parameters this node
// has observed so far. The token store never overwrites a public parameters entry, so it
// keeps the full history and can be queried by hash.
//
//go:generate counterfeiter -o mock/pps.go -fake-name PublicParamsStore . PublicParamsStore
type PublicParamsStore interface {
	// PublicParamsHashes returns the hashes of all the stored public parameters, most recent first.
	PublicParamsHashes(ctx context.Context) ([]driver.PPHash, error)
	// PublicParamsByHash returns the raw public parameters whose hash matches the passed one.
	PublicParamsByHash(ctx context.Context, hash driver.PPHash) ([]byte, error)
}

// PublicParamsHistory maps a commitment token format back to the public parameters that
// produced it.
//
// The format of a zkatdlog output digests the Pedersen generators (see
// token.SupportedTokenFormat), so regenerating the public parameters with different
// generators renames every previously created token. Such a token can only be opened with
// the Pedersen bases of the generation that created it, which is what this type resolves.
//
// The format recorded on the ledger is always the authority here, never a hash somebody
// hands over: ByHashAndFormat only accepts public parameters that demonstrably generate the
// format of the token being opened, so a peer cannot make the issuer open a commitment with
// bases of its choosing.
type PublicParamsHistory struct {
	logger logging.Logger
	store  PublicParamsStore

	mutex    sync.RWMutex
	byHash   map[string]*setup.PublicParams
	byFormat map[token.Format]*resolvedPublicParams
}

// resolvedPublicParams is a generation of public parameters together with the hash it is
// stored under.
type resolvedPublicParams struct {
	pp   *setup.PublicParams
	hash driver.PPHash
}

// NewPublicParamsHistory returns a PublicParamsHistory backed by the passed store.
// A nil logger falls back to the package default.
func NewPublicParamsHistory(logger logging.Logger, store PublicParamsStore) *PublicParamsHistory {
	if logger == nil {
		logger = logging.MustGetLogger()
	}

	return &PublicParamsHistory{
		logger:   logger,
		store:    store,
		byHash:   map[string]*setup.PublicParams{},
		byFormat: map[token.Format]*resolvedPublicParams{},
	}
}

// ByHashAndFormat returns the public parameters stored under the passed hash, after checking
// that they really generate the passed token format. It fails if this node no longer holds
// those public parameters, or if they do not generate that format.
func (h *PublicParamsHistory) ByHashAndFormat(ctx context.Context, hash driver.PPHash, format token.Format) (*setup.PublicParams, error) {
	if len(hash) == 0 {
		return nil, errors.Errorf("no public parameters hash provided for token format [%s]", format)
	}
	if len(format) == 0 {
		return nil, errors.New("no token format provided")
	}

	pp, err := h.byHashCached(ctx, hash)
	if err != nil {
		return nil, err
	}
	formats, err := token2.CommTokenFormats(pp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed computing the token formats of public parameters [%s]", hash)
	}
	if !slices.Contains(formats, format) {
		return nil, errors.Errorf("public parameters [%s] do not generate token format [%s]", hash, format)
	}

	// Prime the by-format cache so that a later ByFormat for the same format does not rescan
	// every stored generation. An existing entry is left alone: ByFormat resolves a format to
	// the most recent generation that produces it, and that choice must not be overridden by
	// whichever hash a request happened to carry.
	h.mutex.Lock()
	if _, ok := h.byFormat[format]; !ok {
		h.byFormat[format] = &resolvedPublicParams{pp: pp, hash: hash}
	}
	h.mutex.Unlock()

	return pp, nil
}

// ByFormat scans the stored public parameters for the generation that produced the passed
// token format, returning them together with the hash they are stored under. It fails if no
// stored generation generates that format.
func (h *PublicParamsHistory) ByFormat(ctx context.Context, format token.Format) (*setup.PublicParams, driver.PPHash, error) {
	if len(format) == 0 {
		return nil, nil, errors.New("no token format provided")
	}

	h.mutex.RLock()
	cached, ok := h.byFormat[format]
	h.mutex.RUnlock()
	if ok {
		return cached.pp, cached.hash, nil
	}

	hashes, err := h.store.PublicParamsHashes(ctx)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to list the stored public parameters")
	}
	skipped := 0
	for _, hash := range hashes {
		pp, err := h.byHashCached(ctx, hash)
		if err != nil {
			// a single unusable entry must not hide the generation we are looking for, but it
			// must not vanish either: a pruned or corrupted entry is the likeliest reason a
			// token cannot be traced back to its public parameters
			h.logger.WarnfContext(ctx, "skipping unusable public parameters [%s] while resolving token format [%s]: [%v]", hash, format, err)
			skipped++

			continue
		}
		formats, err := token2.CommTokenFormats(pp)
		if err != nil {
			h.logger.WarnfContext(ctx, "cannot compute the token formats of public parameters [%s]: [%v]", hash, err)
			skipped++

			continue
		}
		if !slices.Contains(formats, format) {
			continue
		}

		h.mutex.Lock()
		h.byFormat[format] = &resolvedPublicParams{pp: pp, hash: hash}
		h.mutex.Unlock()

		return pp, hash, nil
	}

	if skipped != 0 {
		return nil, nil, errors.Errorf(
			"no stored public parameters generate token format [%s]; [%d] of [%d] stored entries were unusable, see the warnings above",
			format,
			skipped,
			len(hashes),
		)
	}

	return nil, nil, errors.Errorf("no stored public parameters generate token format [%s]", format)
}

// byHashCached loads, validates and caches the public parameters stored under the passed hash.
func (h *PublicParamsHistory) byHashCached(ctx context.Context, hash driver.PPHash) (*setup.PublicParams, error) {
	key := string(hash)

	h.mutex.RLock()
	pp, ok := h.byHash[key]
	h.mutex.RUnlock()
	if ok {
		return pp, nil
	}

	raw, err := h.store.PublicParamsByHash(ctx, hash)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load the public parameters with hash [%s]", hash)
	}
	if len(raw) == 0 {
		return nil, errors.Wrapf(ErrPublicParamsNotFound, "no public parameters stored with hash [%s]", hash)
	}
	pp, err = setup.NewPublicParamsFromBytes(raw, setup.DLogNoGHDriverName, setup.ProtocolV1)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize the public parameters with hash [%s]", hash)
	}
	if err := pp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "invalid public parameters with hash [%s]", hash)
	}

	h.mutex.Lock()
	h.byHash[key] = pp
	h.mutex.Unlock()

	return pp, nil
}
