/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skis"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	identitydriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// stats holds counters accumulated during a Run.
type stats struct {
	total      int // total signer entries processed
	notInToken int // entries whose identity was NOT found in the tokens table
	errors     int // entries that produced an error during processing
	skipped    int // entries found in the tokens table (not orphaned)
	kept       int // entries not found, whose SKIs are all in the owned-skis keep-list
}

// Run iterates all entries in the Signers table in batches of batchSize.
// For each signer entry whose identity is NOT found in the Tokens table,
// it derives the SKIs and prints those not in the owned-skis keep-list to stdout.
// It builds the keep-list first, writing its statistics and errors to errOut;
// if that fails, it prints no entry and returns the error.
func Run(ctx context.Context, stores *Stores, batchSize int, errOut io.Writer) error {
	keepList, err := skis.KeepList(ctx, &skis.SQLOwnerSource{
		DB:              stores.ReadDB,
		TokensTable:     stores.TableNames.Tokens,
		CondInterpreter: stores.CondInterpreter,
	}, skis.NewOwnedSKIExtractor(stores.Identity), errOut)
	if err != nil {
		return errors.Wrap(err, "build owned-skis keep-list")
	}
	keep := make(map[string]struct{}, len(keepList))
	for _, ski := range keepList {
		keep[ski] = struct{}{}
	}
	extractor := skis.NewSKIExtractor(stores.Identity)

	var s stats
	offset := 0
	for {
		entries, err := stores.Identity.IterateSigners(ctx, offset, batchSize)
		if err != nil {
			return errors.Wrapf(err, "iterate signers at offset %d", offset)
		}

		for _, entry := range entries {
			s.total++
			result, err := processEntry(ctx, entry, stores, extractor, keep)
			if err != nil {
				s.errors++
				// Log and continue — a single bad entry should not abort the whole scan.
				fmt.Printf("WARN: identity %s: %v\n", entry.IdentityHash, err)

				continue
			}
			switch {
			case result.kept:
				s.kept++
			case result.notInToken:
				s.notInToken++
			default:
				s.skipped++
			}
		}

		if len(entries) < batchSize {
			// Last page reached.
			break
		}

		offset += batchSize
	}

	fmt.Printf("\n--- Statistics ---\n")
	fmt.Printf("  Total identities processed : %d\n", s.total)
	fmt.Printf("  Not found in tokens table  : %d\n", s.notInToken)
	fmt.Printf("  Found in tokens table      : %d\n", s.skipped)
	fmt.Printf("  Kept by owned-skis         : %d\n", s.kept)
	fmt.Printf("  Errors during processing   : %d\n", s.errors)

	return nil
}

// entryResult holds per-entry outcome flags returned by processEntry.
type entryResult struct {
	notInToken bool // identity was not found in the tokens table
	kept       bool // not found directly, but all its SKIs are in the owned-skis keep-list
}

func processEntry(
	ctx context.Context,
	entry identitydriver.SignerEntry,
	stores *Stores,
	extractor *cleanup.SKIExtractor,
	keep map[string]struct{},
) (entryResult, error) {
	typed, err := identity.UnmarshalTypedIdentity(entry.Identity)
	if err != nil {
		// UnmarshalTypedIdentity failed: the raw bytes are not a wrapped TypedIdentity.
		// Try idemixnym first, then idemix as a fallback.
		for _, candidate := range []struct {
			ownerType string
			idType    identity.Type
		}{
			{idemixnym.IdentityTypeString, idemixnym.IdentityType},
			{idemix.IdentityTypeString, idemix.IdentityType},
		} {
			t := &identity.TypedIdentity{Type: candidate.idType, Identity: entry.Identity}
			// A lookup error is a storage failure, not a wrong type guess: report it.
			found, lookupErr := hasToken(ctx, stores, t, candidate.ownerType)
			if lookupErr != nil {
				return entryResult{}, lookupErr
			}
			result, retryErr := runWithTyped(ctx, entry, extractor, keep, t, candidate.ownerType, found)
			if retryErr == nil {
				return result, nil
			}
		}
		// Both candidates failed; report the original unmarshal error.
		return entryResult{}, errors.Wrap(err, "unmarshal identity")
	}

	ownerType := identity.TypeToString(typed.Type)
	found, err := hasToken(ctx, stores, typed, ownerType)
	if err != nil {
		return entryResult{}, err
	}

	return runWithTyped(ctx, entry, extractor, keep, typed, ownerType, found)
}

// hasToken reports whether typed owns a token, directly, with the given owner type.
func hasToken(ctx context.Context, stores *Stores, typed *identity.TypedIdentity, ownerType string) (bool, error) {
	found, err := stores.Token.HasTokenForIdentity(ctx, typed.Identity, ownerType)
	if err != nil {
		return false, errors.Wrap(err, "token lookup")
	}
	if !found && ownerType == boolpolicy.PolicyString {
		// Older Tokens rows carry the legacy policy owner type.
		found, err = stores.Token.HasTokenForIdentity(ctx, typed.Identity, skis.LegacyPolicyOwnerType)
		if err != nil {
			return false, errors.Wrap(err, "token lookup with legacy policy owner type")
		}
	}

	return found, nil
}

func runWithTyped(
	ctx context.Context,
	entry identitydriver.SignerEntry,
	extractor *cleanup.SKIExtractor,
	keep map[string]struct{},
	typed *identity.TypedIdentity,
	ownerType string,
	found bool,
) (entryResult, error) {
	if found {
		// Signer has tokens — not orphaned.
		return entryResult{notInToken: false}, nil
	}

	derived, err := extractor.GetSKIsFromIdentity(ctx, typed.Identity, ownerType)
	if err != nil {
		return entryResult{notInToken: true}, errors.Wrap(err, "extract SKIs")
	}
	// Drop the SKIs owned-skis keeps: they belong to a token owner, e.g. a composite containing this signer.
	remaining := slices.DeleteFunc(slices.Clone(derived), func(ski string) bool {
		_, ok := keep[ski]

		return ok
	})
	if len(derived) > 0 && len(remaining) == 0 {
		return entryResult{kept: true}, nil
	}

	fmt.Printf("[%s][%s]: [%s]\n", entry.IdentityHash, ownerType, strings.Join(remaining, ", "))

	return entryResult{notInToken: true}, nil
}
